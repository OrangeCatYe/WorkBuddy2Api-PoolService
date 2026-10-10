// Package httpx HTTP 公共工具：JSON 输出、OpenAI 错误结构、常量时间鉴权。
package httpx

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// WriteJSON 输出 JSON。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// OpenAIError OpenAI 兼容错误体。
type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}

// ErrorType 按状态码推导 OpenAI 错误类型。
func ErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	default:
		return "invalid_request_error"
	}
}

// WriteOpenAIError 输出 OpenAI 错误；retryAfter>0 时附 Retry-After（秒，向上取整）。
func WriteOpenAIError(w http.ResponseWriter, status int, code, msg string, retryAfter time.Duration) {
	if retryAfter > 0 {
		secs := int((retryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	WriteJSON(w, status, map[string]any{"error": OpenAIError{
		Message: msg, Type: ErrorType(status), Code: code,
	}})
}

// BearerToken 提取 Authorization: Bearer / x-api-key。
func BearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			return strings.TrimSpace(h[7:])
		}
		return strings.TrimSpace(h)
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

// SecretEqual 常量时间比较（先取摘要，避免长度泄露）。
func SecretEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// SecurityHeaders 面板与管理接口的安全响应头。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; "+
				"script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}
