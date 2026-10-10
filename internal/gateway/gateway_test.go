package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
	"github.com/orangeruan/poolservice/internal/providers/mock"
)

func newTestGateway(t *testing.T) (*Gateway, *http.ServeMux) {
	t.Helper()
	reg := provider.NewRegistry()
	if err := reg.Register(mock.New()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Gateway.LegacyAPIKey = "sk-test"
	cat := router.NewCatalog(reg, 0)
	g := New(func() *config.Config { return cfg }, reg, cat, nil)
	mux := http.NewServeMux()
	g.Register(mux)
	return g, mux
}

// ttswitch（model_fetch.rs）识别的规范字段必须全部存在。
func TestModelsTTSwitchFields(t *testing.T) {
	_, mux := newTestGateway(t)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var think map[string]any
	for _, m := range resp.Data {
		if m["id"] == "mock-think" {
			think = m
		}
	}
	if think == nil {
		t.Fatalf("mock-think missing: %s", rec.Body.String())
	}
	for _, k := range []string{"display_name", "context_window", "max_output_tokens", "input_modalities", "supports_tools", "reasoning_levels"} {
		if _, ok := think[k]; !ok {
			t.Errorf("field %s missing", k)
		}
	}
	if mods := think["input_modalities"].([]any); len(mods) != 2 || mods[1] != "image" {
		t.Errorf("input_modalities = %v", mods)
	}
}

func TestModelsRequireKey(t *testing.T) {
	_, mux := newTestGateway(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "authentication_error") {
		t.Fatalf("want openai error type: %s", rec.Body.String())
	}
}

func TestChatEffortMapping(t *testing.T) {
	_, mux := newTestGateway(t)
	body := `{"model":"mock/mock-think","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body)).WithContext(context.Background())
	req.Header.Set("Authorization", "Bearer sk-test")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Reasoning-Effort-Applied"); got != "high" {
		t.Fatalf("applied effort = %q, want high", got)
	}
	if !strings.Contains(rec.Body.String(), "effort=high") {
		t.Fatalf("provider did not receive mapped effort: %s", rec.Body.String())
	}
}

func TestChatUnknownModel(t *testing.T) {
	_, mux := newTestGateway(t)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(`{"model":"nope","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
