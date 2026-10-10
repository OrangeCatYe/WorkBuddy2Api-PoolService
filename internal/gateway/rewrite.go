package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

// respRewriter 包装 Provider 的响应输出：
//   - 把响应里的 model 字段回显为客户端请求时的模型名（ttswitch 依赖它做路由与计费归因）
//   - 采集 usage（含缓存命中、积分）用于统计
//
// SSE 按行处理，遇到 data: 行逐帧改写，Flush 透传，保证流式不被缓冲；
// 非流式 JSON 整体缓冲，Finish 时改写一次后写出。
type respRewriter struct {
	w       http.ResponseWriter
	model   string
	status  int
	started time.Time
	ttfb    time.Duration

	mode     int // 0 未定 / 1 SSE / 2 JSON 缓冲 / 3 透传
	lineBuf  bytes.Buffer
	jsonBuf  bytes.Buffer
	usage    provider.Usage
	hasUsage bool
	errMsg   string
	sawDone  bool
}

func newRespRewriter(w http.ResponseWriter, model string) *respRewriter {
	return &respRewriter{w: w, model: model, started: time.Now()}
}

func (r *respRewriter) Header() http.Header { return r.w.Header() }

func (r *respRewriter) WriteHeader(code int) {
	if r.status != 0 {
		return
	}
	r.status = code
	ct := strings.ToLower(r.w.Header().Get("Content-Type"))
	switch {
	case strings.Contains(ct, "text/event-stream"):
		r.mode = 1
	case strings.Contains(ct, "json"):
		r.mode = 2
		r.w.Header().Del("Content-Length")
		return // 缓冲到 Finish 再写头
	default:
		r.mode = 3
	}
	r.w.WriteHeader(code)
}

func (r *respRewriter) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if r.ttfb == 0 {
		r.ttfb = time.Since(r.started)
	}
	switch r.mode {
	case 1:
		r.lineBuf.Write(b)
		r.drainLines(false)
		return len(b), nil
	case 2:
		return r.jsonBuf.Write(b)
	default:
		return r.w.Write(b)
	}
}

func (r *respRewriter) Flush() {
	if r.mode == 1 {
		r.drainLines(false)
	}
	if r.mode == 2 {
		return
	}
	if f, ok := r.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Finish 写出剩余缓冲（Provider 返回后调用）。
func (r *respRewriter) Finish() {
	switch r.mode {
	case 1:
		r.drainLines(true)
		if f, ok := r.w.(http.Flusher); ok {
			f.Flush()
		}
	case 2:
		body := r.jsonBuf.Bytes()
		if out, ok := r.rewriteJSON(body); ok {
			body = out
		}
		r.w.WriteHeader(r.status)
		_, _ = r.w.Write(body)
	}
}

func (r *respRewriter) drainLines(final bool) {
	for {
		data := r.lineBuf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if final && len(data) > 0 {
				r.writeLine(append([]byte(nil), data...), false)
				r.lineBuf.Reset()
			}
			return
		}
		line := append([]byte(nil), data[:i]...)
		r.lineBuf.Next(i + 1)
		r.writeLine(line, true)
	}
}

func (r *respRewriter) writeLine(line []byte, newline bool) {
	trimmed := bytes.TrimRight(line, "\r")
	if p, ok := bytes.CutPrefix(trimmed, []byte("data:")); ok {
		payload := bytes.TrimSpace(p)
		if string(payload) == "[DONE]" {
			r.sawDone = true
		} else if len(payload) > 0 && payload[0] == '{' {
			if out, ok := r.rewriteJSON(payload); ok {
				line = append([]byte("data: "), out...)
			}
		}
	}
	_, _ = r.w.Write(line)
	if newline {
		_, _ = r.w.Write([]byte{'\n'})
	}
}

// rewriteJSON 改写 model 并采集 usage / error；解析失败返回 false（原样透传）。
func (r *respRewriter) rewriteJSON(b []byte) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, false
	}
	if u, ok := obj["usage"].(map[string]any); ok {
		r.captureUsage(u)
	}
	if e, ok := obj["error"].(map[string]any); ok {
		if m, _ := e["message"].(string); m != "" {
			r.errMsg = m
		}
	}
	if _, ok := obj["model"]; !ok || r.model == "" {
		return nil, false
	}
	obj["model"] = r.model
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}

func num(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	}
	return 0, false
}

func (r *respRewriter) captureUsage(u map[string]any) {
	r.hasUsage = true
	r.usage = parseUsage(u)
}

// Unwrap 供 http.ResponseController 使用。
func (r *respRewriter) Unwrap() http.ResponseWriter { return r.w }
