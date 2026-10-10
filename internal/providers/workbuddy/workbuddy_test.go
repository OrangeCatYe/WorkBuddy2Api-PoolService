package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

func TestParseCreditRate(t *testing.T) {
	cases := map[string]float64{"x0.05 credits": 0.05, "x0.29": 0.29, "0.50x": 0.5, "0x": 0, "x1": 1, " X2 ": 2}
	for in, want := range cases {
		got, ok := ParseCreditRate(in)
		if !ok || got != want {
			t.Errorf("ParseCreditRate(%q)=(%v,%v) want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "credits", "abc", "x-1"} {
		if _, ok := ParseCreditRate(bad); ok {
			t.Errorf("ParseCreditRate(%q) should fail", bad)
		}
	}
	if got := FormatCreditRate(0.29 * 0.5); got != "x0.145" {
		t.Errorf("FormatCreditRate = %s", got)
	}
}

func TestSettingsDefaults(t *testing.T) {
	s, err := ParseSettings(nil, "data")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled || s.AuthDir != filepath.Join("data", "workbuddy", "auths") || s.StateFile != filepath.Join("data", "workbuddy", "state.json") {
		t.Fatalf("defaults: %+v", s)
	}
	if s.softRate.Seconds() != 600 || s.expiringSoon.Hours() != 168 || s.packageRefresh.Minutes() != 30 {
		t.Fatalf("durations: %v %v %v", s.softRate, s.expiringSoon, s.packageRefresh)
	}
	// 旧项目配置段可直接搬入
	raw := json.RawMessage(`{"auth_dir":"../old/auths","pool":{"credit_floor":100},"schedule":{"checkin_hours":[8]}}`)
	s, err = ParseSettings(raw, "data")
	if err != nil || s.AuthDir != "../old/auths" || s.Pool.CreditFloor != 100 || s.Schedule.CheckinHours[0] != 8 {
		t.Fatalf("override: %+v %v", s, err)
	}
	if _, err := ParseSettings(json.RawMessage(`{"schedule":{"checkin_hours":[25]}}`), "d"); err == nil {
		t.Fatal("invalid hour must fail")
	}
	if _, err := ParseSettings(json.RawMessage(`{"prompt":{"mode":"weird"}}`), "d"); err == nil {
		t.Fatal("invalid prompt mode must fail")
	}
}

func TestParseImported(t *testing.T) {
	nested := `{"auth":{"accessToken":"at","refreshToken":"rt","expiresAt":1,"domain":"copilot.tencent.com"},"account":{"uid":"u-1","nickname":"n"}}`
	a, err := parseImported(json.RawMessage(nested))
	if err != nil || a.UID != "u-1" || a.AccessToken != "at" {
		t.Fatalf("nested: %+v %v", a, err)
	}
	cockpit := `{"uid":"abc_2","access_token":"at","refresh_token":"rt","expires_at":1700000000000,"email":"e@x","domain":"www.workbuddy.ai"}`
	a, err = parseImported(json.RawMessage(cockpit))
	if err != nil || a.UID != "abc_2" || a.Nickname != "e@x" || a.ExpiresAt != 1700000000 || a.Realm() != "global" {
		t.Fatalf("cockpit: %+v realm=%s %v", a, a.Realm(), err)
	}
	if _, err := parseImported(json.RawMessage(`{"accessToken":"at","uid":"../evil"}`)); err == nil {
		t.Fatal("path traversal uid must be rejected")
	}
}

func TestEngineModel(t *testing.T) {
	for in, want := range map[string]string{"glm-5.2": "cn:glm-5.2", "cn:glm-5.2": "cn:glm-5.2", "global:gpt-5.4": "global:gpt-5.4"} {
		if got := engineModel(in); got != want {
			t.Errorf("engineModel(%q)=%q", in, got)
		}
	}
}

// 零账号时整条链路可装配、可启动，聊天返回 OpenAI 错误而非 panic，且不访问上游。
func TestProviderEmptyPool(t *testing.T) {
	dir := t.TempDir()
	s, err := ParseSettings(json.RawMessage(`{"schedule":{"balance_refresh_enabled":false}}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); p.Stop() }()

	var _ provider.ChatCapable = p
	var _ provider.LoginCapable = p
	var _ provider.AccountOps = p
	var _ provider.TaskCapable = p
	var _ provider.QuotaCapable = p
	var _ provider.SettingsCapable = p
	if caps := provider.MetaWithCaps(p).Capabilities; len(caps) != 6 {
		t.Fatalf("caps = %v", caps)
	}
	if n := len(p.Accounts()); n != 0 {
		t.Fatalf("accounts = %d", n)
	}
	if ms, err := p.Models(ctx); err != nil || len(ms) != 0 {
		t.Fatalf("models = %v %v", ms, err)
	}
	rec := httptest.NewRecorder()
	out := p.ServeChat(ctx, &provider.ChatRequest{
		Model: "glm-5.2", Body: []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`),
	}, rec)
	if out.Status < 500 || !bytes.Contains(rec.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("status=%d body=%s", out.Status, rec.Body.String())
	}
}
