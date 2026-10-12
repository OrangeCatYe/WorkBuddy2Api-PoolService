package credits

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)

func at(days float64) *time.Time {
	t := t0.Add(time.Duration(days * float64(day)))
	return &t
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestSimulate(t *testing.T) {
	pkgs := []account.Package{
		{Name: "c", Amount: 300, Remaining: 300, ExpireAt: at(8)},
		{Name: "a", Amount: 100, Remaining: 94, ExpireAt: at(2)},
		{Name: "gone", Amount: 100, Remaining: 50, ExpireAt: at(-1)}, // 已过期，忽略
		{Name: "used", Amount: 100, Remaining: 0, ExpireAt: at(1)},   // 已用完，忽略
		{Name: "b", Amount: 100, Remaining: 100, ExpireAt: at(4)},
		{Name: "forever", Amount: 50, Remaining: 50},
	}
	// 每天 40：2 天能用 80（a 浪费 14）；到第 4 天累计容量 160-80=80（b 浪费 20）；
	// 到第 8 天累计容量 320-160=160（c 浪费 140）；永久 50 全部可用。
	s := Simulate(pkgs, 544, 40, t0)
	if len(s.Packages) != 4 || s.Packages[0].Name != "a" || s.Packages[3].Name != "forever" {
		t.Fatalf("order = %+v", s.Packages)
	}
	if !near(s.Packages[0].Waste, 14) || !near(s.Packages[1].Waste, 20) || !near(s.Packages[2].Waste, 140) || s.Packages[3].Waste != 0 {
		t.Fatalf("waste = %+v", s.Packages)
	}
	if !near(s.Waste, 174) || !s.FirstWasteAt.Equal(*at(2)) {
		t.Fatalf("total waste %v first %v", s.Waste, s.FirstWasteAt)
	}
	// 零浪费所需：max(94/2, 194/4, 494/8) = 61.75
	if !near(s.RequiredPerDay, 61.75) {
		t.Fatalf("required = %v", s.RequiredPerDay)
	}
	// 可用掉的 80+80+160+50 = 370，每天 40 → 9.25 天耗尽
	if s.ExhaustAt == nil || s.ExhaustAt.Sub(t0) != time.Duration(9.25*float64(day)) {
		t.Fatalf("exhaust = %v", s.ExhaustAt)
	}
	if s.Expiring72h != 94 || s.Expiring7d != 194 || s.Expiring30d != 494 || s.NoExpiry != 50 || s.NextExpireAmount != 94 {
		t.Fatalf("buckets = %+v", s)
	}

	// 零消耗：所有会过期的积分都浪费，不预测耗尽时间
	z := Simulate(pkgs, 544, 0, t0)
	if !near(z.Waste, 494) || z.ExhaustAt != nil {
		t.Fatalf("zero burn = %v %v", z.Waste, z.ExhaustAt)
	}
	// 超过预测范围的包视为长期余额
	far := Simulate([]account.Package{{Remaining: 500, ExpireAt: at(3000)}, {Remaining: 10, ExpireAt: at(5)}}, 510, 0, t0)
	if far.Waste != 10 || far.NoExpiry != 500 || !near(far.RequiredPerDay, 2) {
		t.Fatalf("far = %+v", far)
	}
}

func TestNormalizePackages(t *testing.T) {
	pkgs := []account.Package{
		{Name: "late", Remaining: 100, ExpireAt: at(9)},
		{Name: "early", Remaining: 30, ExpireAt: at(1)},
	}
	// 实时余额比明细少 50：从最早到期的扣（early 扣完，late 再扣 20）
	got := normalizePackages(pkgs, 80, t0)
	if len(got) != 1 || got[0].Name != "late" || got[0].Remaining != 80 {
		t.Fatalf("shrink = %+v", got)
	}
	// 余额比明细多：补一个不过期的差额
	got = normalizePackages(pkgs, 200, t0)
	if len(got) != 3 || got[2].ExpireAt != nil || got[2].Remaining != 70 {
		t.Fatalf("grow = %+v", got)
	}
	// 没有明细：整个余额视为不过期
	if s := Simulate(nil, 500, 10, t0); s.Waste != 0 || s.NoExpiry != 500 {
		t.Fatalf("no packages = %+v", s)
	}
}

func TestLedgerBurn(t *testing.T) {
	l := NewLedger(t0.AddDate(0, 0, -2)) // 服务运行了 2 天
	l.Add("p/a", t0.AddDate(0, 0, -1), 30)
	l.Add("p/a", t0, 10)
	l.Add("p/a", t0.AddDate(0, 0, -20), 999) // 窗口外
	burn, obs := l.Burn("p/a", t0, 7)
	// since 被 -20 天的记录拉早 → 观测 7 天；窗口内 40
	if !near(obs, 7) || !near(burn, 40.0/7) {
		t.Fatalf("burn=%v obs=%v", burn, obs)
	}
	if today, n := l.Today("p/a", t0); today != 10 || n != 1 {
		t.Fatalf("today = %v %d", today, n)
	}
	if s := l.Series("p/a", t0, 3); s[0] != 0 || s[1] != 30 || s[2] != 10 {
		t.Fatalf("series = %v", s)
	}
	l2 := NewLedger(t0.Add(-time.Hour)) // 刚启动 1 小时：按 6 小时折算
	l2.Add("p/a", t0, 5)
	if burn, _ := l2.Burn("p/a", t0, 7); !near(burn, 20) {
		t.Fatalf("short window burn = %v", burn)
	}
}

// fakeProvider 两个账号：hot 有大量 3 天内到期积分，cold 没有到期压力。
type fakeProvider struct {
	bias     func(string, string) float64
	observer func(provider.PickEvent)
	reserve  float64
}

func (f *fakeProvider) Meta() provider.Meta { return provider.Meta{ID: "fp", Name: "fake"} }
func (f *fakeProvider) Accounts() []account.Account {
	return []account.Account{
		{ID: "hot", Provider: "fp", Label: "Hot", Status: account.StatusActive, Quota: &account.Quota{Remaining: 500, Total: 500,
			Packages: []account.Package{{Remaining: 400, ExpireAt: at(2)}, {Remaining: 100, ExpireAt: at(60)}}}},
		{ID: "cold", Provider: "fp", Label: "Cold", Status: account.StatusActive, Quota: &account.Quota{Remaining: 500, Total: 500,
			Packages: []account.Package{{Remaining: 500, ExpireAt: at(200)}}}},
	}
}
func (f *fakeProvider) SetRoutingBias(fn func(string, string) float64) { f.bias = fn }
func (f *fakeProvider) SetPickObserver(fn func(provider.PickEvent))    { f.observer = fn }
func (f *fakeProvider) SetCreditReserve(n float64)                     { f.reserve = n }

func TestServiceBiasAndDecisions(t *testing.T) {
	reg := provider.NewRegistry()
	fp := &fakeProvider{}
	_ = reg.Register(fp)
	cfg := config.Default()
	cfg.Credits.Reserve = 50
	s := New(func() *config.Config { return cfg }, reg, nil, NewLedger(t0.AddDate(0, 0, -7)))
	s.now = func() time.Time { return t0 }
	s.Attach()
	s.Refresh(context.Background())
	s.rates = map[string]float64{"fp/free": 0, "fp/cheap": 0.1, "fp/pricey": 1.6}

	snap := s.Snapshot()
	if snap.Accounts[0].AccountID != "hot" || snap.Accounts[0].Urgency != 1 || snap.Accounts[1].Urgency > 0.001 {
		t.Fatalf("urgency = %+v", snap.Accounts)
	}
	// hot：2 天到期 400 + 60 天到期 100 均浪费（零消耗）；cold 的 200 天包超出预测范围，视为长期余额
	if !near(snap.Pool.Expiring72h, 400) || !near(snap.Pool.Waste, 500) || len(snap.Calendar) != 30 || fp.reserve != 50 {
		t.Fatalf("pool = %+v reserve=%v", snap.Pool, fp.reserve)
	}
	if b := fp.bias("hot", "free"); b != 1 {
		t.Fatalf("free bias = %v", b)
	}
	if b := fp.bias("hot", "cheap"); b != 4 {
		t.Fatalf("cheap bias = %v", b)
	}
	if b := fp.bias("hot", "pricey"); b != 8 {
		t.Fatalf("pricey bias = %v", b)
	}
	if b := fp.bias("cold", "pricey"); !near(b, 1) {
		t.Fatalf("cold bias = %v", b)
	}
	if b := fp.bias("hot", "unknown-model"); b != 4 { // 未知倍率按普通收费模型
		t.Fatalf("unknown bias = %v", b)
	}

	fp.observer(provider.PickEvent{Time: t0, Provider: "fp", Model: "pricey", Chosen: "hot", Candidates: []provider.PickCandidate{
		{AccountID: "cold", Weight: 10, Bias: 1}, {AccountID: "hot", Weight: 80, Bias: 8},
	}})
	fp.observer(provider.PickEvent{Time: t0, Provider: "fp", Model: "free", Chosen: "cold", Candidates: []provider.PickCandidate{
		{AccountID: "cold", Weight: 10, Bias: 1}, {AccountID: "hot", Weight: 10, Bias: 1},
	}})
	ds := s.Decisions(10)
	if len(ds) != 2 || ds[0].Model != "free" || ds[1].ChosenLabel != "Hot" || !near(ds[1].Candidates[0].Share, 80.0/90) {
		t.Fatalf("decisions = %+v", ds)
	}
	if ds[1].Reason == "" || ds[0].Reason != "免费模型不消耗积分，按平台原算法均衡" {
		t.Fatalf("reasons = %q / %q", ds[1].Reason, ds[0].Reason)
	}

	// 每日上限：今日已用超过上限 → 降权
	cfg.Credits.DailyCap = 5
	s.ledger.Add("fp/hot", t0, 6)
	s.Refresh(context.Background())
	s.rates = map[string]float64{"fp/cheap": 0.1}
	if b := fp.bias("hot", "cheap"); b != 0.05 {
		t.Fatalf("capped bias = %v", b)
	}
	// 关闭策略
	cfg.Credits.Strategy = "off"
	s.Refresh(context.Background())
	if b := fp.bias("hot", "cheap"); b != 1 {
		t.Fatalf("off bias = %v", b)
	}
}

// 空池子必须返回空数组而不是 null：nil slice 经 JSON 序列化后是 null，
// 前端直接 .map 会整页崩溃（新用户未添加账号时的 /credits 白屏）。
func TestEmptySnapshotSerializesToArrays(t *testing.T) {
	cfg := config.Default()
	s := New(func() *config.Config { return cfg }, provider.NewRegistry(), nil, NewLedger(t0))
	s.now = func() time.Time { return t0 }

	// 初始快照：Refresh 尚未跑过时也不能是 null
	if b, err := json.Marshal(s.Snapshot()); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(b), "null") {
		t.Fatalf("初始快照含 null：%s", b)
	}

	s.Refresh(context.Background())
	b, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Fatalf("空账号快照含 null：%s", b)
	}
	for _, field := range []string{`"accounts":[]`, `"calendar":[`, `"tunable_providers":[]`} {
		if !strings.Contains(string(b), field) {
			t.Fatalf("缺少 %s：%s", field, b)
		}
	}
}
