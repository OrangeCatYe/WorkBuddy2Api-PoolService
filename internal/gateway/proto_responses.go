package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

// ---------- 入站：OpenAI Responses → Chat ----------

// responsesToChat 转换请求体（无状态：不支持 previous_response_id）。
func responsesToChat(in map[string]any) (map[string]any, bool, error) {
	model, _ := in["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, false, errors.New("field 'model' is required")
	}
	if prev, _ := in["previous_response_id"].(string); prev != "" {
		return nil, false, errors.New("previous_response_id is not supported; send the full conversation in 'input' (store=false)")
	}
	out := map[string]any{"model": model}
	var msgs []any
	if s, _ := in["instructions"].(string); s != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": s})
	}

	switch input := in["input"].(type) {
	case string:
		msgs = append(msgs, map[string]any{"role": "user", "content": input})
	case []any:
		var pendingCalls []any
		var pendingReasoning []string
		flushCalls := func() {
			if len(pendingCalls) == 0 {
				return
			}
			m := map[string]any{"role": "assistant", "content": nil, "tool_calls": pendingCalls}
			if len(pendingReasoning) > 0 {
				m["reasoning_content"] = strings.Join(pendingReasoning, "\n")
				pendingReasoning = nil
			}
			msgs = append(msgs, m)
			pendingCalls = nil
		}
		for _, ri := range input {
			it, ok := ri.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := it["type"].(string)
			switch {
			case typ == "function_call":
				args, _ := it["arguments"].(string)
				if args == "" {
					args = "{}"
				}
				pendingCalls = append(pendingCalls, map[string]any{
					"id": it["call_id"], "type": "function",
					"function": map[string]any{"name": it["name"], "arguments": args},
				})
			case typ == "function_call_output":
				flushCalls()
				msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": it["call_id"], "content": outputText(it["output"])})
			case typ == "reasoning":
				if sum, ok := it["summary"].([]any); ok {
					if t := joinTextBlocks(sum); t != "" {
						pendingReasoning = append(pendingReasoning, t)
					}
				}
			case typ == "message" || (typ == "" && it["role"] != nil):
				flushCalls()
				role, _ := it["role"].(string)
				if role == "developer" {
					role = "system"
				}
				m := responsesMessage(role, it["content"])
				if role == "assistant" && len(pendingReasoning) > 0 {
					m["reasoning_content"] = strings.Join(pendingReasoning, "\n")
					pendingReasoning = nil
				}
				msgs = append(msgs, m)
			}
		}
		flushCalls()
	default:
		return nil, false, errors.New("field 'input' is required")
	}
	if len(msgs) == 0 {
		return nil, false, errors.New("field 'input' is empty")
	}
	out["messages"] = msgs

	copyNum(in, out, "max_output_tokens", "max_tokens")
	copyNum(in, out, "temperature", "temperature")
	copyNum(in, out, "top_p", "top_p")
	if v, ok := in["parallel_tool_calls"].(bool); ok {
		out["parallel_tool_calls"] = v
	}
	if u, _ := in["user"].(string); u != "" {
		out["user"] = u
	}
	if r, ok := in["reasoning"].(map[string]any); ok {
		if e, _ := r["effort"].(string); e != "" {
			out["reasoning_effort"] = e
		}
	}
	if tools, ok := in["tools"].([]any); ok {
		var ct []any
		for _, rt := range tools {
			t, ok := rt.(map[string]any)
			if !ok || t["type"] != "function" {
				continue // 内置工具（web_search、file_search 等）无法转发
			}
			fn := map[string]any{"name": t["name"]}
			if d, _ := t["description"].(string); d != "" {
				fn["description"] = d
			}
			if p, ok := t["parameters"]; ok && p != nil {
				fn["parameters"] = p
			} else {
				fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			if s, ok := t["strict"].(bool); ok {
				fn["strict"] = s
			}
			ct = append(ct, map[string]any{"type": "function", "function": fn})
		}
		if len(ct) > 0 {
			out["tools"] = ct
		}
	}
	if out["tools"] != nil {
		switch tc := in["tool_choice"].(type) {
		case string:
			out["tool_choice"] = tc
		case map[string]any:
			if tc["type"] == "function" {
				out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": tc["name"]}}
			}
		}
	}
	if tx, ok := in["text"].(map[string]any); ok {
		if f, ok := tx["format"].(map[string]any); ok {
			switch f["type"] {
			case "json_schema":
				js := map[string]any{"name": f["name"], "schema": f["schema"]}
				if s, ok := f["strict"].(bool); ok {
					js["strict"] = s
				}
				out["response_format"] = map[string]any{"type": "json_schema", "json_schema": js}
			case "json_object":
				out["response_format"] = map[string]any{"type": "json_object"}
			}
		}
	}
	stream, _ := in["stream"].(bool)
	if stream {
		out["stream"] = true
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	return out, stream, nil
}

func outputText(v any) string {
	switch o := v.(type) {
	case string:
		return o
	case []any:
		return joinTextBlocks(o)
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func responsesMessage(role string, content any) map[string]any {
	if s, ok := content.(string); ok {
		return map[string]any{"role": role, "content": s}
	}
	parts, _ := content.([]any)
	var out []any
	var texts []string
	hasImage := false
	for _, rp := range parts {
		p, ok := rp.(map[string]any)
		if !ok {
			continue
		}
		switch p["type"] {
		case "input_text", "output_text", "text":
			t, _ := p["text"].(string)
			texts = append(texts, t)
			out = append(out, map[string]any{"type": "text", "text": t})
		case "input_image":
			url, _ := p["image_url"].(string)
			if url != "" && role == "user" {
				img := map[string]any{"url": url}
				if d, _ := p["detail"].(string); d != "" {
					img["detail"] = d
				}
				out = append(out, map[string]any{"type": "image_url", "image_url": img})
				hasImage = true
			}
		}
	}
	if hasImage {
		return map[string]any{"role": role, "content": out}
	}
	return map[string]any{"role": role, "content": strings.Join(texts, "\n\n")}
}

// ---------- 出站：Chat → Responses ----------

type respItem struct {
	kind   string // reasoning / message / function_call
	id     string
	callID string
	name   string
	text   strings.Builder
}

type responsesSink struct {
	w       http.ResponseWriter
	model   string
	stream  bool
	req     map[string]any
	id      string
	created int64
	seq     int
	started bool

	items   []*respItem
	cur     *respItem
	curTool int
	stop    string
	u       provider.Usage
	hasU    bool
}

func newResponsesSink(w http.ResponseWriter, model string, stream bool, req map[string]any) *responsesSink {
	return &responsesSink{w: w, model: model, stream: stream, req: req, curTool: -1}
}

func (s *responsesSink) emit(typ string, data map[string]any) {
	if !s.stream {
		return
	}
	data["type"] = typ
	data["sequence_number"] = s.seq
	s.seq++
	sseEvent(s.w, typ, data)
}

func (s *responsesSink) base(status string) map[string]any {
	r := map[string]any{
		"id": s.id, "object": "response", "created_at": s.created, "status": status,
		"model": s.model, "output": []any{}, "error": nil, "incomplete_details": nil,
		"parallel_tool_calls": true, "tool_choice": "auto", "tools": []any{}, "metadata": map[string]any{},
		"usage": nil,
	}
	for _, k := range []string{"instructions", "tools", "tool_choice", "parallel_tool_calls", "temperature", "top_p", "max_output_tokens", "reasoning", "text", "metadata", "user"} {
		if v, ok := s.req[k]; ok && v != nil {
			r[k] = v
		}
	}
	return r
}

func (s *responsesSink) begin(string) {
	if s.id == "" {
		s.id = "resp_" + newShortID()
		s.created = time.Now().Unix()
	}
	if !s.stream || s.started {
		return
	}
	s.started = true
	startSSE(s.w)
	s.emit("response.created", map[string]any{"response": s.base("in_progress")})
	s.emit("response.in_progress", map[string]any{"response": s.base("in_progress")})
}

func (s *responsesSink) index(it *respItem) int {
	for i, x := range s.items {
		if x == it {
			return i
		}
	}
	return -1
}

func (s *responsesSink) itemJSON(it *respItem, status string) map[string]any {
	switch it.kind {
	case "reasoning":
		sum := []any{}
		if it.text.Len() > 0 {
			sum = append(sum, map[string]any{"type": "summary_text", "text": it.text.String()})
		}
		return map[string]any{"type": "reasoning", "id": it.id, "summary": sum}
	case "function_call":
		return map[string]any{"type": "function_call", "id": it.id, "call_id": it.callID, "name": it.name, "arguments": it.text.String(), "status": status}
	}
	content := []any{}
	if status == "completed" {
		content = append(content, map[string]any{"type": "output_text", "text": it.text.String(), "annotations": []any{}})
	}
	return map[string]any{"type": "message", "id": it.id, "status": status, "role": "assistant", "content": content}
}

func (s *responsesSink) closeItem() {
	it := s.cur
	if it == nil {
		return
	}
	s.cur, s.curTool = nil, -1
	if !s.stream {
		return
	}
	idx := s.index(it)
	switch it.kind {
	case "reasoning":
		part := map[string]any{"type": "summary_text", "text": it.text.String()}
		s.emit("response.reasoning_summary_text.done", map[string]any{"item_id": it.id, "output_index": idx, "summary_index": 0, "text": it.text.String()})
		s.emit("response.reasoning_summary_part.done", map[string]any{"item_id": it.id, "output_index": idx, "summary_index": 0, "part": part})
	case "message":
		part := map[string]any{"type": "output_text", "text": it.text.String(), "annotations": []any{}}
		s.emit("response.output_text.done", map[string]any{"item_id": it.id, "output_index": idx, "content_index": 0, "text": it.text.String()})
		s.emit("response.content_part.done", map[string]any{"item_id": it.id, "output_index": idx, "content_index": 0, "part": part})
	case "function_call":
		s.emit("response.function_call_arguments.done", map[string]any{"item_id": it.id, "output_index": idx, "arguments": it.text.String()})
	}
	s.emit("response.output_item.done", map[string]any{"output_index": idx, "item": s.itemJSON(it, "completed")})
}

func (s *responsesSink) open(it *respItem) {
	s.closeItem()
	s.items = append(s.items, it)
	s.cur = it
	idx := len(s.items) - 1
	s.emit("response.output_item.added", map[string]any{"output_index": idx, "item": s.itemJSON(it, "in_progress")})
	switch it.kind {
	case "reasoning":
		s.emit("response.reasoning_summary_part.added", map[string]any{"item_id": it.id, "output_index": idx, "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}})
	case "message":
		s.emit("response.content_part.added", map[string]any{"item_id": it.id, "output_index": idx, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	}
}

func (s *responsesSink) reasoning(t string) {
	if s.cur == nil || s.cur.kind != "reasoning" {
		s.open(&respItem{kind: "reasoning", id: "rs_" + newShortID()})
	}
	s.cur.text.WriteString(t)
	s.emit("response.reasoning_summary_text.delta", map[string]any{"item_id": s.cur.id, "output_index": len(s.items) - 1, "summary_index": 0, "delta": t})
}

func (s *responsesSink) text(t string) {
	if s.cur == nil || s.cur.kind != "message" {
		s.open(&respItem{kind: "message", id: "msg_" + newShortID()})
	}
	s.cur.text.WriteString(t)
	s.emit("response.output_text.delta", map[string]any{"item_id": s.cur.id, "output_index": len(s.items) - 1, "content_index": 0, "delta": t, "logprobs": []any{}})
}

func (s *responsesSink) tool(idx int, id, name, args string) {
	if s.cur == nil || s.cur.kind != "function_call" || s.curTool != idx {
		if id == "" {
			id = "call_" + newShortID()
		}
		s.open(&respItem{kind: "function_call", id: "fc_" + newShortID(), callID: id, name: name})
		s.curTool = idx
	}
	if args != "" {
		s.cur.text.WriteString(args)
		s.emit("response.function_call_arguments.delta", map[string]any{"item_id": s.cur.id, "output_index": len(s.items) - 1, "delta": args})
	}
}

func (s *responsesSink) finish(r string) { s.stop = r }

func (s *responsesSink) usage(u provider.Usage) { s.u, s.hasU = u, true }

func (s *responsesSink) final() map[string]any {
	status := "completed"
	if s.stop == "length" {
		status = "incomplete"
	}
	r := s.base(status)
	out := make([]any, 0, len(s.items))
	for _, it := range s.items {
		out = append(out, s.itemJSON(it, "completed"))
	}
	r["output"] = out
	if status == "incomplete" {
		r["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	r["usage"] = map[string]any{
		"input_tokens":          s.u.PromptTokens,
		"input_tokens_details":  map[string]any{"cached_tokens": s.u.CachedTokens},
		"output_tokens":         s.u.CompletionTokens,
		"output_tokens_details": map[string]any{"reasoning_tokens": s.u.ReasoningTokens},
		"total_tokens":          s.u.PromptTokens + s.u.CompletionTokens,
	}
	return r
}

func (s *responsesSink) end() {
	s.closeItem()
	r := s.final()
	if s.stream {
		ev := "response.completed"
		if r["status"] == "incomplete" {
			ev = "response.incomplete"
		}
		s.emit(ev, map[string]any{"response": r})
		return
	}
	writeJSONNoEscape(s.w, http.StatusOK, r)
}

func (s *responsesSink) fail(status int, typ, msg string) {
	if !s.started {
		writeOpenAIErrorFmt(s.w, status, typ, msg)
		return
	}
	s.closeItem()
	r := s.base("failed")
	code := "server_error"
	if status == 429 {
		code = "rate_limit_exceeded"
	}
	r["error"] = map[string]any{"code": code, "message": msg}
	s.emit("response.failed", map[string]any{"response": r})
}

func writeOpenAIErrorFmt(w http.ResponseWriter, status int, typ, msg string) {
	if typ == "" {
		typ = openAIErrType(status)
	}
	writeJSONNoEscape(w, status, map[string]any{"error": map[string]any{"message": msg, "type": typ, "code": nil}})
}

func openAIErrType(status int) string {
	switch {
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 404:
		return "not_found_error"
	case status == 429:
		return "rate_limit_error"
	case status >= 500:
		return "api_error"
	}
	return "invalid_request_error"
}
