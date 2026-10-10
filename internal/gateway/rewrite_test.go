package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
)

func TestRewriterSSEModelEchoAndUsage(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newRespRewriter(rec, "glm-5.2(x0.05)")
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.WriteHeader(200)
	// 故意把一帧拆成两次写入，验证跨块的行缓冲
	_, _ = rw.Write([]byte("data: {\"id\":\"a\",\"model\":\"cn:glm-5.2\",\"choices\":[{\"delta\":{\"content\":\"<b>&\"}}]}\n\nda"))
	_, _ = rw.Write([]byte("ta: {\"model\":\"cn:glm-5.2\",\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":8},\"credit\":0.42}}\n\ndata: [DONE]\n\n"))
	rw.Flush()
	rw.Finish()

	out := rec.Body.String()
	if strings.Contains(out, "cn:glm-5.2") {
		t.Fatalf("model not rewritten: %s", out)
	}
	if strings.Count(out, `"model":"glm-5.2(x0.05)"`) != 2 {
		t.Fatalf("expected 2 rewritten frames: %s", out)
	}
	if !strings.Contains(out, `<b>&`) {
		t.Fatalf("html must not be escaped: %s", out)
	}
	if !strings.HasSuffix(out, "data: [DONE]\n\n") {
		t.Fatalf("done frame lost: %q", out)
	}
	if !rw.hasUsage || rw.usage.PromptTokens != 12 || rw.usage.CachedTokens != 8 || rw.usage.TotalTokens != 15 || !rw.usage.HasCredit || rw.usage.Credit != 0.42 {
		t.Fatalf("usage = %+v", rw.usage)
	}
}

func TestRewriterJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newRespRewriter(rec, "mock-think")
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Content-Length", "999")
	rw.WriteHeader(200)
	_, _ = rw.Write([]byte(`{"model":"upstream-x","id":"c1","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"n":12345678901234567}`))
	rw.Finish()
	if rec.Header().Get("Content-Length") != "" {
		t.Fatal("stale content-length kept")
	}
	var obj map[string]any
	d := json.NewDecoder(rec.Body)
	d.UseNumber()
	if err := d.Decode(&obj); err != nil {
		t.Fatal(err)
	}
	if obj["model"] != "mock-think" || obj["n"].(json.Number).String() != "12345678901234567" {
		t.Fatalf("got %v", obj)
	}
}

func TestRewriterErrorPassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newRespRewriter(rec, "m")
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Retry-After", "30")
	rw.WriteHeader(429)
	_, _ = rw.Write([]byte(`{"error":{"message":"all accounts cooling","type":"rate_limit_error"}}`))
	rw.Finish()
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "30" || rw.errMsg != "all accounts cooling" {
		t.Fatalf("code=%d hdr=%v err=%q", rec.Code, rec.Header(), rw.errMsg)
	}
}

func TestModelsCreditSuffix(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(newFakeProvider())
	cfg := config.Default()
	cfg.Gateway.LegacyAPIKey = "k"
	cfg.Gateway.CreditInModelName = true
	g := New(func() *config.Config { return cfg }, reg, router.NewCatalog(reg, 0), nil)
	mux := http.NewServeMux()
	g.Register(mux)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"id":"mock-think(x0.5)"`) || !strings.Contains(rec.Body.String(), `"display_name":"Mock Thinking (x0.5)"`) {
		t.Fatalf("suffix missing: %s", rec.Body.String())
	}

	// 用带后缀的名字请求，响应 model 回显客户端原名
	body := `{"model":"mock-think(x0.5)","messages":[{"role":"user","content":"hi"}]}`
	req = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer k")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"model":"mock-think(x0.5)"`) {
		t.Fatalf("echo failed: %d %s", rec.Code, rec.Body.String())
	}
}
