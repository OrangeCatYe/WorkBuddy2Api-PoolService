package workbuddy

import (
	"encoding/json"
	"testing"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

func TestApplySettings(t *testing.T) {
	s, err := ParseSettings(json.RawMessage(`{"schedule":{"balance_refresh_enabled":false}}`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	defer p.pool.Close()

	// schema 中的每个 Key 都能在配置段里取到值（防止 Key 拼写与 json tag 不一致）
	vals, _ := provider.SettingsValues(p.SettingsRaw(), p.SettingsSchema())
	for _, f := range p.SettingsSchema() {
		if f.Kind != provider.KindSecret && f.Kind != provider.KindText && vals[f.Key] == nil {
			t.Errorf("schema key %s 在配置中不存在", f.Key)
		}
	}

	merged, err := provider.MergeSettings(nil, p.SettingsSchema(), map[string]any{
		"pool.credit_floor":           float64(120),
		"pool.max_in_flight":          float64(7),
		"schedule.checkin_hours":      []any{float64(8)},
		"session_sticky.ttl":          "2h",
		"global.enabled":              false,
		"logging.request_client_info": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := p.ApplySettings(merged)
	if err != nil {
		t.Fatal(err)
	}
	if p.pool.CreditFloor() != 120 {
		t.Fatalf("credit floor = %d", p.pool.CreditFloor())
	}
	if p.live.Load().RecordClientInfo {
		t.Fatal("live config not updated")
	}
	run := p.settings()
	if run.Pool.MaxInFlight != 7 || run.Schedule.CheckinHours[0] != 8 {
		t.Fatalf("hot fields not applied: %+v", run.Pool)
	}
	// 需重启的字段：运行值保持不变，并报告为待重启
	if run.SessionSticky.TTL != "30m" || !run.Global.Enabled {
		t.Fatalf("restart fields changed at runtime: ttl=%s global=%v", run.SessionSticky.TTL, run.Global.Enabled)
	}
	if len(pending) != 2 || pending[0] != "session_sticky.ttl" || pending[1] != "global.enabled" {
		t.Fatalf("pending = %v", pending)
	}
	if pr := p.PendingRestart(); len(pr) != 2 {
		t.Fatalf("PendingRestart = %v", pr)
	}
	// 回显已保存值（包括尚未生效的需重启字段）
	if got := p.SettingsRaw(); len(provider.DiffKeys(got, merged, []string{"pool.max_in_flight", "session_sticky.ttl"})) != 0 {
		t.Fatal("raw should reflect hot value")
	}

	// 积分中心保底优先于平台配置
	p.SetCreditReserve(300)
	if _, err := p.ApplySettings(merged); err != nil || p.pool.CreditFloor() != 300 {
		t.Fatalf("reserve override lost: %d %v", p.pool.CreditFloor(), err)
	}
	p.SetCreditReserve(0)
	if p.pool.CreditFloor() != 120 {
		t.Fatalf("reserve reset = %d", p.pool.CreditFloor())
	}

	// 校验失败不改运行状态
	bad, _ := provider.MergeSettings(merged, p.SettingsSchema(), map[string]any{
		"pool.max_in_flight": float64(1), "pool.breaker_cooldown": "6h", "pool.breaker_cooldown_max": "1h",
	})
	if _, err := p.ApplySettings(bad); err == nil {
		t.Fatal("expected validation error")
	}
	if p.settings().Pool.MaxInFlight != 7 {
		t.Fatal("failed apply must not change state")
	}
}
