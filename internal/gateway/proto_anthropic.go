package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

// ---------- 入站：Anthropic Messages → OpenAI Chat ----------

// anthropicToChat 转换请求体。返回 chat 请求对象与 stream 标志。
func anthropicToChat(in map[string]any) (map[string]any, bool, error) {
	model, _ := in["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, false, errors.New("field 'model' is required")
	}
	out := map[string]any{"model": model}
	var msgs []any

	switch s := in["system"].(type) {
	case string:
		if s != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": s})
		}
	case []any:
		if t := joinTextBlocks(s); t != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": t})
		}
	}

	raw, _ := in["messages"].([]any)
	if len(raw) == 0 {
		return nil, false, errors.New("field 'messages' is required")
	}
	for _, rm := range raw {
		m, ok := rm.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		conv, err := anthropicMessage(role, m["content"])
		if err != nil {
			return nil, false, err
		}
		msgs = append(msgs, conv...)
	}
	out["messages"] = msgs

	copyNum(in, out, "max_tokens", "max_tokens")
	copyNum(in, out, "temperature", "temperature")
	copyNum(in, out, "top_p", "top_p")
	if st, ok := in["stop_sequences"].([]any); ok && len(st) > 0 {
		out["stop"] = st
	}
	if md, ok := in["metadata"].(map[string]any); ok {
		if uid, _ := md["user_id"].(string); uid != "" {
			out["user"] = uid
		}
	}

	if tools, ok := in["tools"].([]any); ok && len(tools) > 0 {
		var ct []any
		for _, rt := range tools {
			t, ok := rt.(map[string]any)
			if !ok {
				continue
			}
			name, _ := t["name"].(string)
			if name == "" {
				continue // 服务端工具（web_search 等）无法转发
			}
			if typ, _ := t["type"].(string); typ != "" && typ != "custom" && !strings.HasPrefix(typ, "function") {
				continue
			}
			fn := map[string]any{"name": name}
			if d, _ := t["description"].(string); d != "" {
				fn["description"] = d
			}
			if sc, ok := t["input_schema"]; ok {
				fn["parameters"] = sc
			} else {
				fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			ct = append(ct, map[string]any{"type": "function", "function": fn})
		}
		if len(ct) > 0 {
			out["tools"] = ct
		}
	}
	if tc, ok := in["tool_choice"].(map[string]any); ok && out["tools"] != nil {
		switch tc["type"] {
		case "auto":
			out["tool_choice"] = "auto"
		case "any":
			out["tool_choice"] = "required"
		case "none":
			out["tool_choice"] = "none"
		case "tool":
			out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": tc["name"]}}
		}
		if dp, ok := tc["disable_parallel_tool_use"].(bool); ok && dp {
			out["parallel_tool_calls"] = false
		}
	}

	// 推理强度：显式 effort 优先，其次按 thinking.budget_tokens 推断。
	if oc, ok := in["output_config"].(map[string]any); ok {
		if e, _ := oc["effort"].(string); e != "" {
			out["reasoning_effort"] = e
		}
	}
	if e, _ := in["reasoning_effort"].(string); e != "" {
		out["reasoning_effort"] = e
	}
	if th, ok := in["thinking"].(map[string]any); ok && out["reasoning_effort"] == nil {
		switch th["type"] {
		case "enabled":
			b, _ := num(th["budget_tokens"])
			out["reasoning_effort"] = effortForBudget(int64(b))
		case "adaptive":
			out["reasoning_effort"] = "medium"
		}
	}

	stream, _ := in["stream"].(bool)
	if stream {
		out["stream"] = true
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	return out, stream, nil
}

// effortForBudget thinking.budget_tokens → 推理档位。
func effortForBudget(b int64) string {
	switch {
	case b <= 0:
		return "medium"
	case b < 4096:
		return "low"
	case b < 16384:
		return "medium"
	case b < 32768:
		return "high"
	default:
		return "xhigh"
	}
}

func copyNum(in, out map[string]any, from, to string) {
	if v, ok := in[from]; ok && v != nil {
		if _, isNum := num(v); isNum {
			out[to] = v
		}
	}
}

func joinTextBlocks(blocks []any) string {
	var parts []string
	for _, rb := range blocks {
		if b, ok := rb.(map[string]any); ok {
			if t, _ := b["text"].(string); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func anthropicImage(b map[string]any) (map[string]any, bool) {
	src, ok := b["source"].(map[string]any)
	if !ok {
		return nil, false
	}
	var url string
	switch src["type"] {
	case "base64":
		mt, _ := src["media_type"].(string)
		data, _ := src["data"].(string)
		url = "data:" + mt + ";base64," + data
	case "url":
		url, _ = src["url"].(string)
	}
	if url == "" {
		return nil, false
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}, true
}

// anthropicMessage 一条 Anthropic 消息可能拆成多条 Chat 消息（tool_result → role=tool）。
func anthropicMessage(role string, content any) ([]any, error) {
	if role != "user" && role != "assistant" {
		return nil, fmt.Errorf("unsupported role %q", role)
	}
	if s, ok := content.(string); ok {
		return []any{map[string]any{"role": role, "content": s}}, nil
	}
	blocks, _ := content.([]any)
	var out []any
	var parts []any
	var text []string
	var reasoning []string
	var calls []any
	hasImage := false

	flushUser := func() {
		if len(parts) == 0 {
			return
		}
		if hasImage {
			out = append(out, map[string]any{"role": "user", "content": parts})
		} else {
			var ss []string
			for _, p := range parts {
				ss = append(ss, p.(map[string]any)["text"].(string))
			}
			out = append(out, map[string]any{"role": "user", "content": strings.Join(ss, "\n\n")})
		}
		parts, hasImage = nil, false
	}

	for _, rb := range blocks {
		b, ok := rb.(map[string]any)
		if !ok {
			continue
		}
		switch b["type"] {
		case "text":
			t, _ := b["text"].(string)
			if role == "user" {
				parts = append(parts, map[string]any{"type": "text", "text": t})
			} else {
				text = append(text, t)
			}
		case "image":
			if img, ok := anthropicImage(b); ok && role == "user" {
				parts = append(parts, img)
				hasImage = true
			}
		case "thinking":
			if t, _ := b["thinking"].(string); t != "" {
				reasoning = append(reasoning, t)
			}
		case "tool_use":
			args, _ := marshalNoEscape(b["input"])
			if len(args) == 0 || string(args) == "null" {
				args = []byte("{}")
			}
			calls = append(calls, map[string]any{
				"id": b["id"], "type": "function",
				"function": map[string]any{"name": b["name"], "arguments": string(args)},
			})
		case "tool_result":
			var txt string
			switch c := b["content"].(type) {
			case string:
				txt = c
			case []any:
				txt = joinTextBlocks(c)
			}
			if isErr, _ := b["is_error"].(bool); isErr && txt != "" {
				txt = "[tool error] " + txt
			}
			// tool 消息必须紧跟 assistant 的 tool_calls，所以排在同条消息的文本之前。
			out = append(out, map[string]any{"role": "tool", "tool_call_id": b["tool_use_id"], "content": txt})
		}
	}

	if role == "user" {
		flushUser()
		return out, nil
	}
	msg := map[string]any{"role": "assistant", "content": strings.Join(text, "")}
	if len(reasoning) > 0 {
		msg["reasoning_content"] = strings.Join(reasoning, "\n")
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
		if len(text) == 0 {
			msg["content"] = nil
		}
	}
	return []any{msg}, nil
}

// ---------- 出站：OpenAI Chat → Anthropic Messages ----------

type anthropicSink struct {
	w      http.ResponseWriter
	model  string
	stream bool

	id       string
	started  bool
	blocks   []map[string]any // 非流式累计
	cur      int              // 当前块下标，-1 = 无
	curKind  string
	curTool  int
	toolArgs map[int]*strings.Builder
	stop     string
	u        provider.Usage
	hasU     bool
}

func newAnthropicSink(w http.ResponseWriter, model string, stream bool) *anthropicSink {
	return &anthropicSink{w: w, model: model, stream: stream, cur: -1, curTool: -1, toolArgs: map[int]*strings.Builder{}}
}

func (s *anthropicSink) begin(id string) {
	s.id = "msg_" + newShortID()
	if !s.stream || s.started {
		return
	}
	s.started = true
	startSSE(s.w)
	sseEvent(s.w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": s.id, "type": "message", "role": "assistant", "model": s.model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

func (s *anthropicSink) closeBlock() {
	if s.cur < 0 {
		return
	}
	if s.curKind == "tool_use" {
		if b := s.toolArgs[s.curTool]; b != nil {
			var input any = map[string]any{}
			if b.Len() > 0 {
				var v any
				if json.Unmarshal([]byte(b.String()), &v) == nil {
					input = v
				}
			}
			s.blocks[s.cur]["input"] = input
		}
	}
	if s.stream {
		sseEvent(s.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": s.cur})
	}
	s.cur, s.curKind, s.curTool = -1, "", -1
}

func (s *anthropicSink) open(kind string, block map[string]any) {
	s.closeBlock()
	s.blocks = append(s.blocks, block)
	s.cur, s.curKind = len(s.blocks)-1, kind
	if s.stream {
		start := map[string]any{}
		for k, v := range block {
			start[k] = v
		}
		if kind == "tool_use" {
			start["input"] = map[string]any{}
		}
		sseEvent(s.w, "content_block_start", map[string]any{"type": "content_block_start", "index": s.cur, "content_block": start})
	}
}

func (s *anthropicSink) delta(d map[string]any) {
	if s.stream {
		sseEvent(s.w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": s.cur, "delta": d})
	}
}

func (s *anthropicSink) reasoning(t string) {
	if s.curKind != "thinking" {
		s.open("thinking", map[string]any{"type": "thinking", "thinking": "", "signature": ""})
	}
	s.blocks[s.cur]["thinking"] = s.blocks[s.cur]["thinking"].(string) + t
	s.delta(map[string]any{"type": "thinking_delta", "thinking": t})
}

func (s *anthropicSink) text(t string) {
	if s.curKind != "text" {
		s.open("text", map[string]any{"type": "text", "text": ""})
	}
	s.blocks[s.cur]["text"] = s.blocks[s.cur]["text"].(string) + t
	s.delta(map[string]any{"type": "text_delta", "text": t})
}

func (s *anthropicSink) tool(idx int, id, name, args string) {
	if s.curKind != "tool_use" || s.curTool != idx {
		if id == "" {
			id = "toolu_" + newShortID()
		}
		s.open("tool_use", map[string]any{"type": "tool_use", "id": id, "name": name})
		s.curTool = idx
		s.toolArgs[idx] = &strings.Builder{}
	}
	if args != "" {
		s.toolArgs[idx].WriteString(args)
		s.delta(map[string]any{"type": "input_json_delta", "partial_json": args})
	}
}

func (s *anthropicSink) finish(reason string) { s.stop = reason }

func (s *anthropicSink) usage(u provider.Usage) { s.u, s.hasU = u, true }

func anthropicStop(r string) string {
	switch r {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "refusal"
	}
	return "end_turn"
}

func (s *anthropicSink) usageObj() map[string]any {
	in := s.u.PromptTokens - s.u.CachedTokens
	if in < 0 {
		in = 0
	}
	return map[string]any{
		"input_tokens": in, "output_tokens": s.u.CompletionTokens,
		"cache_read_input_tokens": s.u.CachedTokens, "cache_creation_input_tokens": 0,
	}
}

func (s *anthropicSink) end() {
	s.closeBlock()
	stop := anthropicStop(s.stop)
	if s.stop == "" && hasToolUse(s.blocks) {
		stop = "tool_use"
	}
	if s.stream {
		sseEvent(s.w, "message_delta", map[string]any{
			"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": s.usageObj(),
		})
		sseEvent(s.w, "message_stop", map[string]any{"type": "message_stop"})
		return
	}
	content := make([]any, 0, len(s.blocks))
	for _, b := range s.blocks {
		content = append(content, b)
	}
	writeJSONNoEscape(s.w, http.StatusOK, map[string]any{
		"id": s.id, "type": "message", "role": "assistant", "model": s.model,
		"content": content, "stop_reason": stop, "stop_sequence": nil, "usage": s.usageObj(),
	})
}

func hasToolUse(blocks []map[string]any) bool {
	for _, b := range blocks {
		if b["type"] == "tool_use" {
			return true
		}
	}
	return false
}

func anthropicErrType(status int) string {
	switch {
	case status == 400 || status == 413 || status == 422:
		return "invalid_request_error"
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 404:
		return "not_found_error"
	case status == 429:
		return "rate_limit_error"
	case status == 503 || status == 529:
		return "overloaded_error"
	}
	return "api_error"
}

func (s *anthropicSink) fail(status int, _ string, msg string) {
	body := map[string]any{"type": "error", "error": map[string]any{"type": anthropicErrType(status), "message": msg}}
	if s.started {
		sseEvent(s.w, "error", body)
		return
	}
	writeJSONNoEscape(s.w, status, body)
}

// writeAnthropicError 分发前错误（鉴权、模型不存在等）。
func writeAnthropicError(w http.ResponseWriter, status int, _ string, msg string) {
	writeJSONNoEscape(w, status, map[string]any{"type": "error", "error": map[string]any{"type": anthropicErrType(status), "message": msg}})
}

// estimateTokens 粗略估算 token 数（count_tokens 用）：中日韩字符按 1、其他按 4 字符 1 个。
func estimateTokens(v any) int64 {
	b, _ := json.Marshal(v)
	var cjk, other int64
	for _, r := range string(b) {
		if r >= 0x2E80 {
			cjk++
		} else {
			other++
		}
	}
	return cjk + other/4 + 1
}
