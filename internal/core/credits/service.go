package credits

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
)

// AccountForecast 单账号预测。
type AccountForecast struct {
	Provider     string    `json:"provider"`
	AccountID    string    `json:"account_id"`
	Label        string    `json:"label"`
	Status       string    `json:"status"`
	Remaining    float64   `json:"remaining"`
	Total        float64   `json:"total"`
	BurnPerDay   float64   `json:"burn_per_day"`
	ObservedDays float64   `json:"observed_days"`
	TodayCredit  float64   `json:"today_credit"`
	TodayReqs    int64     `json:"today_requests"`
	Series       []float64 `json:"series"`  // 最近 14 天每日消耗
	Urgency      float64   `json:"urgency"` // 0~1：将浪费 / 快到期压力
	Capped       bool      `json:"capped,omitempty"`
	Note         string    `json:"note,omitempty"`
	Sim
}

// PoolForecast 全池汇总。
type PoolForecast struct {
	Remaining      float64    `json:"remaining"`
	BurnPerDay     float64    `json:"burn_per_day"`
	TodayCredit    float64    `json:"today_credit"`
	Expiring24h    float64    `json:"expiring_24h"`
	Expiring72h    float64    `json:"expiring_72h"`
	Expiring7d     float64    `json:"expiring_7d"`
	Expiring30d    float64    `json:"expiring_30d"`
	Waste          float64    `json:"waste"`       // 各账号按自身速度消耗时的预计浪费合计
	IdealWaste     float64    `json:"ideal_waste"` // 全池统一按到期调度时的预计浪费
	RequiredPerDay float64    `json:"required_per_day"`
	ExhaustAt      *time.Time `json:"exhaust_at,omitempty"`
	NextExpireAt   *time.Time `json:"next_expire_at,omitempty"`
	Accounts       int        `json:"accounts"`
}

// Snapshot 一次完整预测。
type Snapshot struct {
	UpdatedAt    time.Time            `json:"updated_at"`
	HistorySince time.Time            `json:"history_since"`
	WindowDays   int                  `json:"window_days"`
	Strategy     config.CreditsConfig `json:"strategy"`
	Pool         PoolForecast         `json:"pool"`
	Calendar     []CalendarDay        `json:"calendar"`
	Accounts     []AccountForecast    `json:"accounts"`
	Tunable      []string             `json:"tunable_providers"` // 策略可生效的平台
}

// DecisionCandidate 决策中的一个候选。
type DecisionCandidate struct {
	AccountID string  `json:"account_id"`
	Label     string  `json:"label"`
	Weight    float64 `json:"weight"`
	Bias      float64 `json:"bias"`
	Share     float64 `json:"share"` // 被选中概率（近似：权重占比）
	Tier      int     `json:"tier"`
	Note      string  `json:"note,omitempty"`
}

// Decision 一次选号决策。
type Decision struct {
	Time        time.Time           `json:"time"`
	Provider    string              `json:"provider"`
	Model       string              `json:"model"`
	Rate        float64             `json:"rate"` // <0 未知
	Chosen      string              `json:"chosen"`
	ChosenLabel string              `json:"chosen_label"`
	Fallback    bool                `json:"fallback,omitempty"`
	Sticky      bool                `json:"sticky,omitempty"`
	Explored    bool                `json:"explored,omitempty"`
	Reason      string              `json:"reason"`
	Candidates  []DecisionCandidate `json:"candidates,omitempty"`
}

type acctState struct {
	label   string
	urgency float64
	capped  bool
	note    string
}

// Service 积分服务。
type Service struct {
	cfg     func() *config.Config
	reg     *provider.Registry
	catalog *router.Catalog
	ledger  *Ledger
	now     func() time.Time

	mu       sync.RWMutex
	snap     Snapshot
	state    map[string]acctState // provider/account
	rates    map[string]float64   // provider/model → 倍率（缺失 = 未知）
	strategy config.CreditsConfig

	decMu     sync.Mutex
	decisions []Decision
	decNext   int
	decFull   bool

	reserveApplied map[string]float64
	kick           chan struct{}
}

const decisionCap = 300

// urgencyTau 到期压力的衰减时间常数（天）：2 天后到期的权重约为 7 天后的 2.7 倍。
const urgencyTau = 5.0

// New 创建。ledger 由调用方用请求归档预热。
func New(cfg func() *config.Config, reg *provider.Registry, catalog *router.Catalog, ledger *Ledger) *Service {
	s := &Service{
		cfg: cfg, reg: reg, catalog: catalog, ledger: ledger, now: time.Now,
		// 初始快照也用空数组：Refresh 尚未跑完时若被读取，字段不会是 null。
		snap:  Snapshot{Accounts: []AccountForecast{}, Calendar: []CalendarDay{}, Tunable: []string{}},
		state: map[string]acctState{}, rates: map[string]float64{},
		decisions: make([]Decision, decisionCap), reserveApplied: map[string]float64{},
		kick: make(chan struct{}, 1),
	}
	s.strategy = cfg().Credits
	return s
}

// Ledger 账本。
func (s *Service) Ledger() *Ledger { return s.ledger }

// Record 网关每个请求结束时调用。
func (s *Service) Record(providerID, accountID string, t time.Time, credit float64, hasCredit bool) {
	if !hasCredit || providerID == "" || accountID == "" {
		return
	}
	s.ledger.Add(providerID+"/"+accountID, t, credit)
}

// Attach 向支持路由调优的 Provider 注入权重与观察回调。
func (s *Service) Attach() {
	for _, p := range s.reg.List() {
		rt, ok := p.(provider.RoutingTunable)
		if !ok {
			continue
		}
		pid := p.Meta().ID
		rt.SetRoutingBias(func(accountID, model string) float64 { return s.Bias(pid, accountID, model) })
		rt.SetPickObserver(s.observe)
	}
}

// Kick 请求尽快刷新（配置变更、手动刷新）。
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run 周期刷新直到 ctx 取消。启动 15 秒后补刷一次：Provider 的积分包明细
// 通常在启动后异步加载，首次刷新时可能只有概要数据。
func (s *Service) Run(ctx context.Context) {
	s.Refresh(ctx)
	warm := time.NewTimer(15 * time.Second)
	defer warm.Stop()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-warm.C:
		case <-t.C:
		case <-s.kick:
		}
		s.Refresh(ctx)
	}
}

// Fresh 返回不早于 maxAge 的预测（过旧则当场重算）。
func (s *Service) Fresh(ctx context.Context, maxAge time.Duration) Snapshot {
	if snap := s.Snapshot(); !snap.UpdatedAt.IsZero() && s.now().Sub(snap.UpdatedAt) < maxAge {
		return snap
	}
	s.Refresh(ctx)
	return s.Snapshot()
}

// Snapshot 最近一次预测。
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Refresh 重新计算预测与选号状态。
func (s *Service) Refresh(ctx context.Context) {
	now := s.now()
	cfg := s.cfg().Credits
	cfg.Normalize()
	window := cfg.BurnWindowDays

	rates := map[string]float64{}
	if s.catalog != nil {
		for _, e := range s.catalog.Entries(ctx) {
			if e.Model.CreditRate >= 0 {
				rates[e.Provider+"/"+e.Model.ID] = e.Model.CreditRate
			}
		}
	}

	// 声明为空数组而非 nil：nil slice 会被序列化成 null，前端直接 .map 会崩。
	accts := []AccountForecast{}
	tunable := []string{}
	for _, p := range s.reg.List() {
		pid := p.Meta().ID
		if _, ok := p.(provider.RoutingTunable); ok {
			tunable = append(tunable, pid)
		}
		for _, a := range p.Accounts() {
			if a.Quota == nil || a.Quota.Unlimited {
				continue
			}
			accts = append(accts, s.forecast(a, now, window))
		}
	}

	// 紧迫度：到期压力 = Σ 积分包余额 × e^(−距到期天数/τ)，再除以全池最大值归一到 0~1。
	// 不用账号自身的「预计浪费」：那取决于账号自己的消耗速度，而速度又被本策略改变，
	// 会形成「被优先 → 速度变快 → 浪费消失 → 不再优先」的振荡。到期压力只看积分包，
	// 等价于全池按到期先后统一调度的理想顺序。
	pressure := make([]float64, len(accts))
	var maxP float64
	for i, a := range accts {
		for _, pk := range a.Packages {
			if pk.ExpireAt != nil {
				pressure[i] += pk.Remaining * math.Exp(-pk.ExpireAt.Sub(now).Hours()/24/urgencyTau)
			}
		}
		if a.Status != string(account.StatusDisabled) {
			maxP = math.Max(maxP, pressure[i])
		}
	}
	state := make(map[string]acctState, len(accts))
	for i := range accts {
		a := &accts[i]
		if maxP > 0 {
			a.Urgency = clamp01(pressure[i] / maxP)
		}
		a.Capped = cfg.DailyCap > 0 && a.TodayCredit >= cfg.DailyCap
		a.Note = noteOf(*a, cfg)
		state[a.Provider+"/"+a.AccountID] = acctState{label: a.Label, urgency: a.Urgency, capped: a.Capped, note: a.Note}
	}
	sort.SliceStable(accts, func(i, j int) bool {
		if accts[i].Urgency != accts[j].Urgency {
			return accts[i].Urgency > accts[j].Urgency
		}
		return accts[i].Remaining > accts[j].Remaining
	})

	snap := Snapshot{UpdatedAt: now, HistorySince: s.ledger.Since(), WindowDays: window, Strategy: cfg, Accounts: accts, Tunable: tunable}
	snap.Pool, snap.Calendar = poolOf(accts, now)
	s.ledger.Prune(now, 31)

	s.mu.Lock()
	s.snap, s.state, s.rates, s.strategy = snap, state, rates, cfg
	s.mu.Unlock()
	s.applyReserve(cfg.Reserve)
}

func (s *Service) applyReserve(n float64) {
	for _, p := range s.reg.List() {
		rt, ok := p.(provider.RoutingTunable)
		if !ok {
			continue
		}
		id := p.Meta().ID
		if v, done := s.reserveApplied[id]; done && v == n {
			continue
		}
		rt.SetCreditReserve(n)
		s.reserveApplied[id] = n
		if n > 0 {
			log.Printf("[credits] %s 余额保底 = %.0f", id, n)
		}
	}
}

func (s *Service) forecast(a account.Account, now time.Time, window int) AccountForecast {
	key := a.Key()
	burn, observed := s.ledger.Burn(key, now, window)
	today, reqs := s.ledger.Today(key, now)
	q := a.Quota
	f := AccountForecast{
		Provider: a.Provider, AccountID: a.ID, Label: a.Label, Status: string(a.Status),
		Remaining: q.Remaining, Total: q.Total, BurnPerDay: burn, ObservedDays: observed,
		TodayCredit: today, TodayReqs: reqs, Series: s.ledger.Series(key, now, 14),
	}
	if f.Label == "" {
		f.Label = shortID(a.ID)
	}
	f.Sim = Simulate(q.Packages, q.Remaining, burn, now)
	return f
}

func poolOf(accts []AccountForecast, now time.Time) (PoolForecast, []CalendarDay) {
	var p PoolForecast
	var merged []account.Package
	var sims []Sim
	for _, a := range accts {
		if a.Status == string(account.StatusDisabled) {
			continue
		}
		p.Accounts++
		p.Remaining += a.Remaining
		p.BurnPerDay += a.BurnPerDay
		p.TodayCredit += a.TodayCredit
		p.Expiring24h += a.Expiring24h
		p.Expiring72h += a.Expiring72h
		p.Expiring7d += a.Expiring7d
		p.Expiring30d += a.Expiring30d
		p.Waste += a.Waste
		sims = append(sims, a.Sim)
		for _, pk := range a.Packages {
			merged = append(merged, account.Package{Name: pk.Name, Remaining: pk.Remaining, Amount: pk.Amount, ExpireAt: pk.ExpireAt})
		}
	}
	ideal := Simulate(merged, p.Remaining, p.BurnPerDay, now)
	p.IdealWaste, p.RequiredPerDay, p.ExhaustAt, p.NextExpireAt = ideal.Waste, ideal.RequiredPerDay, ideal.ExhaustAt, ideal.NextExpireAt
	return p, Calendar(sims, now, 30)
}

func noteOf(a AccountForecast, cfg config.CreditsConfig) string {
	var parts []string
	if a.Capped {
		parts = append(parts, fmt.Sprintf("今日已用 %.0f，达到每日上限", a.TodayCredit))
	}
	if a.Expiring72h > 0 {
		parts = append(parts, fmt.Sprintf("3 天内到期 %.0f", a.Expiring72h))
	} else if a.NextExpireAt != nil {
		parts = append(parts, fmt.Sprintf("最近到期 %s（%.0f）", a.NextExpireAt.Format("01-02"), a.NextExpireAmount))
	}
	if a.Waste >= 1 {
		parts = append(parts, fmt.Sprintf("预计浪费 %.0f", a.Waste))
	}
	if cfg.Reserve > 0 && a.Remaining < cfg.Reserve {
		parts = append(parts, fmt.Sprintf("余额低于保底 %.0f", cfg.Reserve))
	}
	if len(parts) == 0 {
		return "无到期压力"
	}
	return strings.Join(parts, " · ")
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// rateOf 模型倍率；未知返回 -1。调用方需持 s.mu 读锁。
func (s *Service) rateOf(pid, model string) float64 {
	if r, ok := s.rates[pid+"/"+model]; ok {
		return r
	}
	return -1
}

// Bias 账号权重乘数（被 Provider 在选号锁内调用，只读缓存）。
//
//	免费模型（倍率 0）：1，不消耗积分，交给平台原算法均衡；
//	其余：1 + (boost-1) × 紧迫度；高价模型再 ×(1+紧迫度)，让大额消耗集中到将浪费的积分上；
//	超过每日上限：0.05（仍可在无其他候选时兜底）。
func (s *Service) Bias(pid, accountID, model string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.strategy
	if cfg.Strategy == "off" {
		return 1
	}
	rate := s.rateOf(pid, model)
	if rate == 0 {
		return 1
	}
	st, ok := s.state[pid+"/"+accountID]
	if !ok {
		return 1
	}
	if st.capped {
		return 0.05
	}
	b := 1 + (cfg.WasteBoost-1)*st.urgency
	if rate >= cfg.ExpensiveRate {
		b *= 1 + st.urgency
	}
	return b
}

// observe 选号观察回调（Provider 锁内调用）：补充标签与说明后写入环形缓冲。
func (s *Service) observe(ev provider.PickEvent) {
	s.mu.RLock()
	cfg := s.strategy
	d := Decision{Time: ev.Time, Provider: ev.Provider, Model: ev.Model, Rate: s.rateOf(ev.Provider, ev.Model),
		Chosen: ev.Chosen, Fallback: ev.Fallback, Sticky: ev.Sticky, Explored: ev.Explored}
	label := func(id string) (string, acctState) {
		st := s.state[ev.Provider+"/"+id]
		if st.label == "" {
			return shortID(id), st
		}
		return st.label, st
	}
	var sum float64
	for _, c := range ev.Candidates {
		sum += c.Weight
	}
	var chosenNote string
	for _, c := range ev.Candidates {
		l, st := label(c.AccountID)
		dc := DecisionCandidate{AccountID: c.AccountID, Label: l, Weight: c.Weight, Bias: c.Bias, Tier: c.Tier, Note: st.note}
		if sum > 0 {
			dc.Share = c.Weight / sum
		}
		if c.AccountID == ev.Chosen {
			chosenNote = st.note
		}
		d.Candidates = append(d.Candidates, dc)
	}
	sort.SliceStable(d.Candidates, func(i, j int) bool { return d.Candidates[i].Weight > d.Candidates[j].Weight })
	d.ChosenLabel, _ = label(ev.Chosen)
	s.mu.RUnlock()

	switch {
	case ev.Sticky:
		d.Reason = "会话粘性：沿用该会话上次的账号，以命中提示词缓存"
	case ev.Fallback:
		d.Reason = "全部账号冷却中，选最早恢复的账号"
	case ev.Explored:
		d.Reason = "成本探索：改道给未观测过扣费的账号，以发现限免"
	case len(ev.Candidates) == 1:
		d.Reason = "唯一可用账号"
	case cfg.Strategy == "off":
		d.Reason = "积分策略已关闭，按平台原算法加权随机"
	case d.Rate == 0:
		d.Reason = "免费模型不消耗积分，按平台原算法均衡"
	default:
		var bias float64
		for _, c := range d.Candidates {
			if c.AccountID == ev.Chosen {
				bias = c.Bias
			}
		}
		if bias > 1.05 {
			d.Reason = fmt.Sprintf("权重 ×%.1f：%s", bias, chosenNote)
		} else {
			d.Reason = "各账号到期压力相近，按平台原算法加权随机"
		}
	}

	s.decMu.Lock()
	s.decisions[s.decNext] = d
	s.decNext = (s.decNext + 1) % len(s.decisions)
	if s.decNext == 0 {
		s.decFull = true
	}
	s.decMu.Unlock()
}

// Decisions 最近的选号决策（新 → 旧）。
func (s *Service) Decisions(limit int) []Decision {
	s.decMu.Lock()
	defer s.decMu.Unlock()
	n := s.decNext
	if s.decFull {
		n = len(s.decisions)
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Decision, 0, limit)
	for i := 1; i <= limit; i++ {
		out = append(out, s.decisions[(s.decNext-i+len(s.decisions))%len(s.decisions)])
	}
	return out
}
