package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

// chatSink 接收从 OpenAI Chat Completions 输出中解析出的语义事件，
// 由各协议（Anthropic Messages / OpenAI Responses）渲染成自己的格式。
//
// 调用顺序：begin → (reasoning | text | tool)* → finish? → usage? → end；
// 任意时刻可能出现 fail（之后不再有其他事件）。
type chatSink interface {
	begin(id string)
	reasoning(delta string)
	text(delta string)
	tool(index int, id, name, args string)
	finish(reason string)
	usage(u provider.Usage)
	fail(status int, errType, msg string)
	end()
}

// protoWriter 把 Provider 写出的 OpenAI 格式响应翻译为其他协议。
// 位于 respRewriter 之下：client ← protoWriter ← respRewriter ← provider。
type protoWriter struct {
	w      http.ResponseWriter
	sink   chatSink
	hdr    http.Header
	status int
	sse    bool
	buf    bytes.Buffer
	begun  bool
	ended  bool
	failed bool
}

func newProtoWriter(w http.ResponseWriter, sink chatSink) *protoWriter {
	return &protoWriter{w: w, sink: sink, hdr: http.Header{}}
}

// Header 返回影子头：Provider 设置的 Content-Type 等只用于判断格式，
// 真正的响应头由 sink 决定；Retry-After 在 fail 时透传。
func (p *protoWriter) Header() http.Header { return p.hdr }

func (p *protoWriter) WriteHeader(code int) {
	if p.status != 0 {
		return
	}
	p.status = code
	p.sse = strings.Contains(strings.ToLower(p.hdr.Get("Content-Type")), "text/event-stream")
	for _, k := range []string{"Retry-After", "X-Request-Id", "X-Reasoning-Effort-Applied", "X-Reasoning-Effort-Requested"} {
		if v := p.hdr.Get(k); v != "" {
			p.w.Header().Set(k, v)
		}
	}
}

func (p *protoWriter) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.WriteHeader(http.StatusOK)
	}
	p.buf.Write(b)
	if p.sse && p.status < 400 {
		p.drain(false)
	}
	return len(b), nil
}

func (p *protoWriter) Flush() {
	if p.sse && p.status < 400 {
		p.drain(false)
	}
	if f, ok := p.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Finish Provider 返回后调用：处理剩余数据并保证 sink.end / fail 被调用一次。
func (p *protoWriter) Finish() {
	if p.status == 0 {
		p.status = http.StatusBadGateway
	}
	switch {
	case p.status >= 400:
		st, typ, msg := parseErrorBody(p.status, p.buf.Bytes())
		p.doFail(st, typ, msg)
	case p.sse:
		p.drain(true)
	default:
		p.feedJSON(p.buf.Bytes())
	}
	if !p.failed && !p.ended {
		if !p.begun {
			p.begun = true
			p.sink.begin("")
		}
		p.ended = true
		p.sink.end()
	}
	if f, ok := p.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (p *protoWriter) doFail(status int, typ, msg string) {
	if p.failed || p.ended {
		return
	}
	p.failed = true
	p.sink.fail(status, typ, msg)
}

func (p *protoWriter) drain(final bool) {
	for {
		data := p.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if final && len(bytes.TrimSpace(data)) > 0 {
				line := append([]byte(nil), data...)
				p.buf.Reset()
				p.line(line)
			}
			return
		}
		line := append([]byte(nil), data[:i]...)
		p.buf.Next(i + 1)
		p.line(line)
	}
}

func (p *protoWriter) line(line []byte) {
	line = bytes.TrimSpace(line)
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return
	}
	if string(payload) == "[DONE]" {
		if !p.failed && !p.ended {
			if !p.begun {
				p.begun = true
				p.sink.begin("")
			}
			p.ended = true
			p.sink.end()
		}
		return
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if dec.Decode(&obj) != nil {
		return
	}
	p.feedChunk(obj)
}

func (p *protoWriter) start(obj map[string]any) {
	if p.begun {
		return
	}
	p.begun = true
	id, _ := obj["id"].(string)
	p.sink.begin(id)
}

func (p *protoWriter) feedChunk(obj map[string]any) {
	if p.failed || p.ended {
		return
	}
	if e, ok := obj["error"].(map[string]any); ok {
		msg, _ := e["message"].(string)
		typ, _ := e["type"].(string)
		p.doFail(http.StatusBadGateway, typ, msg)
		return
	}
	p.start(obj)
	if chs, ok := obj["choices"].([]any); ok && len(chs) > 0 {
		if c, ok := chs[0].(map[string]any); ok {
			if d, ok := c["delta"].(map[string]any); ok {
				p.feedDelta(d)
			}
			if fr, ok := c["finish_reason"].(string); ok && fr != "" {
				p.sink.finish(fr)
			}
		}
	}
	if u, ok := obj["usage"].(map[string]any); ok {
		p.sink.usage(parseUsage(u))
	}
}

func (p *protoWriter) feedDelta(d map[string]any) {
	for _, k := range []string{"reasoning_content", "reasoning"} {
		if s, ok := d[k].(string); ok && s != "" {
			p.sink.reasoning(s)
			break
		}
	}
	if s, ok := d["content"].(string); ok && s != "" {
		p.sink.text(s)
	}
	if tcs, ok := d["tool_calls"].([]any); ok {
		for i, raw := range tcs {
			tc, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			idx := i
			if n, ok := num(tc["index"]); ok {
				idx = int(n)
			}
			id, _ := tc["id"].(string)
			var name, args string
			if fn, ok := tc["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
				args, _ = fn["arguments"].(string)
			}
			p.sink.tool(idx, id, name, args)
		}
	}
}

// feedJSON 非流式 chat.completion。
func (p *protoWriter) feedJSON(b []byte) {
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&obj) != nil {
		p.doFail(http.StatusBadGateway, "api_error", "invalid upstream response")
		return
	}
	if e, ok := obj["error"].(map[string]any); ok {
		msg, _ := e["message"].(string)
		typ, _ := e["type"].(string)
		p.doFail(http.StatusBadGateway, typ, msg)
		return
	}
	p.start(obj)
	if chs, ok := obj["choices"].([]any); ok && len(chs) > 0 {
		if c, ok := chs[0].(map[string]any); ok {
			if m, ok := c["message"].(map[string]any); ok {
				p.feedDelta(m)
			}
			if fr, ok := c["finish_reason"].(string); ok && fr != "" {
				p.sink.finish(fr)
			}
		}
	}
	if u, ok := obj["usage"].(map[string]any); ok {
		p.sink.usage(parseUsage(u))
	}
	p.ended = true
	p.sink.end()
}

// parseErrorBody 解析 OpenAI 错误体。
func parseErrorBody(status int, b []byte) (int, string, string) {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &env) == nil && env.Error.Message != "" {
		return status, env.Error.Type, env.Error.Message
	}
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = http.StatusText(status)
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return status, "", msg
}

// parseUsage OpenAI usage（含各家缓存字段别名）→ 规范化用量。
func parseUsage(u map[string]any) provider.Usage {
	get := func(keys ...string) int64 {
		for _, k := range keys {
			if f, ok := num(u[k]); ok {
				return int64(f)
			}
		}
		return 0
	}
	var out provider.Usage
	out.PromptTokens = get("prompt_tokens", "input_tokens")
	out.CompletionTokens = get("completion_tokens", "output_tokens")
	out.TotalTokens = get("total_tokens")
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if d, ok := u["prompt_tokens_details"].(map[string]any); ok {
		if f, ok := num(d["cached_tokens"]); ok {
			out.CachedTokens = int64(f)
		}
	}
	if out.CachedTokens == 0 {
		out.CachedTokens = get("prompt_cache_hit_tokens", "cache_read_input_tokens", "cached_tokens")
	}
	if d, ok := u["completion_tokens_details"].(map[string]any); ok {
		if f, ok := num(d["reasoning_tokens"]); ok {
			out.ReasoningTokens = int64(f)
		}
	}
	if out.ReasoningTokens == 0 {
		out.ReasoningTokens = get("completion_thinking_tokens")
	}
	if f, ok := num(u["credit"]); ok {
		out.Credit, out.HasCredit = f, true
	}
	return out
}

// sseEvent 写一条带 event 名的 SSE 事件。
func sseEvent(w http.ResponseWriter, event string, data any) {
	b, _ := marshalNoEscape(data)
	var buf bytes.Buffer
	if event != "" {
		buf.WriteString("event: ")
		buf.WriteString(event)
		buf.WriteByte('\n')
	}
	buf.WriteString("data: ")
	buf.Write(b)
	buf.WriteString("\n\n")
	_, _ = w.Write(buf.Bytes())
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func writeJSONNoEscape(w http.ResponseWriter, status int, v any) {
	b, _ := marshalNoEscape(v)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func startSSE(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}
