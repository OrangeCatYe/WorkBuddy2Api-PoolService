package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// 更新程序靠 /healthz 的 version 确认新版本真的起来了：必须带版本号，且无需鉴权，
// 同时保持原有字段不变（负载均衡、监控已在依赖它们）。
func TestHealthzReportsVersionWithoutAuth(t *testing.T) {
	g, mux := newTestGateway(t)
	g.SetVersion("v9.9.9")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if body["version"] != "v9.9.9" {
		t.Errorf("应报告版本: %v", body)
	}
	if body["service"] != "poolservice" {
		t.Errorf("service 字段必须保持: %v", body)
	}
	for _, k := range []string{"healthy", "total"} {
		if _, ok := body[k]; !ok {
			t.Errorf("原有字段 %s 不能丢", k)
		}
	}
	if rec.Header().Get("X-Service") != "poolservice" {
		t.Error("X-Service 头必须保持")
	}
}
