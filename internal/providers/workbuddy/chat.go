package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

// hopHeaders 不应转发给 engine 的头（逐跳头 + 网关鉴权凭证）。
var hopHeaders = map[string]bool{
	"Authorization": true, "X-Api-Key": true, "Content-Length": true,
	"Connection": true, "Keep-Alive": true, "Transfer-Encoding": true,
	"Upgrade": true, "Te": true, "Trailer": true, "Proxy-Authorization": true,
}

// engineModel 把目录 ID 转为 engine 路由协议：国际版保持 global: 前缀，CN 补 cn:。
func engineModel(id string) string {
	if strings.HasPrefix(id, globalPrefix) || strings.HasPrefix(id, "cn:") {
		return id
	}
	return "cn:" + id
}

// ServeChat 委托 engine 完成选号、上游转发、错误分类与换号重试。
//
// engine 直接向 w 写出 OpenAI 兼容响应；网关在 w 外层负责 model 回显、usage
// 采集与日志。这里只改写 model 字段并补齐客户端来源信息。
func (p *Provider) ServeChat(ctx context.Context, req *provider.ChatRequest, w http.ResponseWriter) provider.ChatOutcome {
	body := req.Body
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err == nil {
		m, _ := json.Marshal(engineModel(req.Model))
		obj["model"] = m
		if b, err := json.Marshal(obj); err == nil {
			body = b
		}
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return provider.ChatOutcome{Status: http.StatusInternalServerError, ErrorMsg: err.Error()}
	}
	for k, vs := range req.Header {
		if !hopHeaders[http.CanonicalHeaderKey(k)] {
			r.Header[k] = append([]string(nil), vs...)
		}
	}
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(body))
	if req.ClientIP != "" {
		r.RemoteAddr = net.JoinHostPort(req.ClientIP, "0")
	}

	sw := &statusWriter{ResponseWriter: w}
	p.handler.ServeHTTP(sw, r)
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	return provider.ChatOutcome{Status: sw.status, Attempts: 1}
}

// statusWriter 记录状态码并透传 Flush（engine 的 SSE 依赖 http.Flusher）。
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
