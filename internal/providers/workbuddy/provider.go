// Package workbuddy 腾讯 CodeBuddy / WorkBuddy 平台适配器。
//
// 协议层（engine/*）原样迁自 workbuddy2api-panel：上游请求改写、错误分类、冷却熔断、
// 会话粘性、签到与成长任务等行为与原项目一致。本包只负责把它装配成 PoolService
// 的 Provider，并把池状态投影为统一账号模型。
package workbuddy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/livecfg"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/pool"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/redisstore"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/reqlog"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/scheduler"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/server"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/session"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/upstream"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/usage"
)

// ID Provider 标识。
const ID = "workbuddy"

// Provider WorkBuddy 适配器。
type Provider struct {
	cfgMu   sync.RWMutex
	cfg     Settings
	started Settings // 启动时的配置，用于计算需重启才生效的改动
	desired Settings // 面板最近保存的完整配置（含尚未生效的需重启字段）
	live    *livecfg.Holder
	reserve float64 // 积分中心设置的余额保底（>0 时覆盖 pool.credit_floor）
	pool    *pool.Pool
	up      *upstream.Client
	store   redisstore.Store
	sess    *session.Router
	sched   *scheduler.Scheduler
	handler *server.Handler
	usage   *usage.Recorder
	reqlog  *reqlog.Recorder

	pkgMu    sync.RWMutex
	packages map[string]pkgSnapshot

	loginMu sync.Mutex
	logins  map[string]loginSession

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type pkgSnapshot struct {
	items []upstream.CreditPackage
	at    time.Time
	err   string
}

// New 按配置装配协议层（与原项目 cmd/server/main.go 装配顺序一致）。
func New(cfg Settings) (*Provider, error) {
	if err := os.MkdirAll(cfg.AuthDir, 0o700); err != nil {
		return nil, fmt.Errorf("workbuddy: mkdir auth dir: %w", err)
	}
	auths, err := auth.LoadDir(cfg.AuthDir)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: load auths: %w", err)
	}
	log.Printf("[workbuddy] loaded %d account(s) from %s", len(auths), cfg.AuthDir)

	p := &Provider{cfg: cfg, started: cfg, desired: cfg, packages: map[string]pkgSnapshot{}, logins: map[string]loginSession{}}
	p.store = redisstore.New(cfg.Upstash.URL, cfg.Upstash.Token)

	pl := pool.New(cfg.StateFile)
	pl.SetStore(p.store)
	pl.RestoreFromSnapshot()
	pl.SyncToDir(auths)
	pl.SetBreaker(cfg.Pool.BreakerThreshold, cfg.breakerCooldown, cfg.breakerCooldownMax)
	pl.SetMaxInFlight(cfg.Pool.MaxInFlight)
	pl.SetMaxInFlightGlobal(cfg.Pool.MaxInFlightGlobal)
	pl.SetDegrade(cfg.Pool.DegradeThreshold, cfg.degradeCooldown, cfg.degradeCooldownMax)
	pl.SetSoftRateMax(cfg.softRateMax)
	pl.SetCostExploreInterval(cfg.costExploreInterval)
	pl.SetCreditFloor(cfg.Pool.CreditFloor)
	pl.SetWeights(cfg.Pool.IdleWeightPerHour, cfg.Pool.IdleWeightMax)
	pl.SetPreferExpiring(cfg.Pool.PreferExpiring)
	p.pool = pl

	redisMode := "noop"
	if _, ok := p.store.(redisstore.Noop); !ok {
		redisMode = "upstash"
	}
	if cfg.SessionSticky.Enabled {
		p.sess = session.New(session.Config{
			TTL:        cfg.sessionTTL,
			GCInterval: cfg.sessionGC,
			Store:      p.store,
			Available:  pl.AvailableUIDs,
			AvailableForModel: func(model string) []string {
				realm, bare := server.ResolveModel(model)
				return pl.WeightedAvailableUIDsForModelRealm(bare, realm)
			},
		})
		p.sess.LoadFromStore()
	}

	up := upstream.New()
	pl.SetModelRateOf(func(realm, model string) string { return up.ModelRate(realm, model) })
	up.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	up.HeaderTimeout = time.Duration(cfg.Upstream.HeaderTimeoutSeconds) * time.Second
	if tr, ok := up.ChatHTTP.Transport.(*http.Transport); ok {
		tr.ResponseHeaderTimeout = up.HeaderTimeout
	}
	up.IdleTimeout = time.Duration(cfg.Upstream.IdleTimeoutSeconds) * time.Second
	up.SanitizeFingerprints.Store(cfg.Features.SanitizeBlacklistFingerprints)
	up.UserAgent = cfg.Upstream.UserAgent
	up.ClientVersion = cfg.Upstream.ClientVersion
	up.CliVersion = cfg.Upstream.CliVersion
	up.ClientName = cfg.Upstream.ClientName
	up.DeviceToken = cfg.Upstream.DeviceToken
	up.DeviceTokenFile = cfg.Upstream.DeviceTokenFile
	up.PassthroughIP = cfg.Upstream.PassthroughIP
	up.GlobalEnabled = cfg.Global.Enabled
	up.ChatBaseGlobal = cfg.Global.ChatBase
	up.BillingBaseGlobal = cfg.Global.BillingBase
	auth.SetGlobalEnabled(cfg.Global.Enabled)
	upstream.SetModelCatalogPath(cfg.stateSibling("model.json"))
	p.up = up

	s := cfg.Schedule
	p.sched = scheduler.New(scheduler.Config{
		Pool: pl, Upstream: up,
		CheckinHours: s.CheckinHours, TravelHours: s.TravelHours, ActivityHours: s.ActivityHours,
		KeepaliveHours: s.KeepaliveHours, BlackcatHours: s.BlackcatHours, GrowthHours: s.GrowthHours,
		ExpiringSoonWindow: cfg.expiringSoon,
		CheckinDisabled:    !s.CheckinEnabled, TravelDisabled: !s.TravelEnabled,
		ActivityDisabled: !s.ActivityEnabled, KeepaliveDisabled: !s.KeepaliveEnabled,
		BlackcatDisabled: !s.BlackcatEnabled, GrowthDisabled: !s.GrowthEnabled,
		IncludeDisabledInTasks: s.IncludeDisabledInTasks,
	})

	p.usage = usage.New(cfg.stateSibling("usage.json"))
	p.reqlog = reqlog.New(reqlog.Config{
		Dir:           cfg.stateSibling("request-logs"),
		Enabled:       cfg.Logging.RequestArchiveEnabled,
		RetentionDays: cfg.Logging.RequestRetentionDays,
		MaxBytes:      int64(cfg.Logging.RequestArchiveMaxMB) << 20,
	})

	// 网关鉴权已由 PoolService 完成：engine 的 api_key 置空（不再二次鉴权）。
	p.live = livecfg.New(livecfg.Snapshot{
		SoftCooldown:         cfg.softRate,
		SanitizeFingerprints: cfg.Features.SanitizeBlacklistFingerprints,
		RecordClientInfo:     cfg.Logging.RequestClientInfo,
	})
	p.handler = server.NewHandler(server.Config{
		Pool: pl, Upstream: up, Session: p.sess,
		StickyCount: func() int {
			if p.sess != nil {
				return p.sess.Count()
			}
			return 0
		},
		RedisMode: redisMode, SoftCooldown: cfg.softRate, Live: p.live,
		Usage: p.usage, RequestLog: p.reqlog,
		PromptMode: cfg.Prompt.Mode, PromptText: cfg.promptText,
		RecordClientInfo: cfg.Logging.RequestClientInfo,
		GlobalEnabled:    cfg.Global.Enabled,
		OnRequestDone:    onEngineDone,
	})
	return p, nil
}

// Start 启动后台协程：定时任务、余额刷新、积分包刷新、会话 GC、倍率预热。
func (p *Provider) Start(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)
	p.usage.Start()
	if p.sess != nil {
		p.sess.StartGC()
	}
	p.wg.Add(1)
	go func() { defer p.wg.Done(); p.sched.Run(ctx) }()
	// 余额刷新循环总是启动：关闭时处于暂停态，面板重新打开后可热恢复。
	st := p.settings()
	minutes := st.Schedule.BalanceRefreshMinutes
	if minutes <= 0 {
		minutes = 5
	}
	p.sched.StartBalanceRefresh(ctx, time.Duration(minutes)*time.Minute)
	if !st.Schedule.BalanceRefreshEnabled {
		p.sched.SetBalanceInterval(0)
	}
	p.wg.Add(1)
	go func() { defer p.wg.Done(); p.packageLoop(ctx) }()
	go p.warmModelRates(ctx)
	return nil
}

// Stop 停止并落盘（顺序同原项目：pool → store）。
func (p *Provider) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	if p.sess != nil {
		p.sess.StopGC()
	}
	p.usage.Stop()
	p.reqlog.Close()
	p.pool.Close()
	_ = p.store.Close()
}

// Meta 元信息。
func (p *Provider) Meta() provider.Meta {
	return provider.Meta{
		ID:          ID,
		Name:        "WorkBuddy",
		Description: "腾讯 CodeBuddy / WorkBuddy 账号（国内版 copilot.tencent.com，国际版 workbuddy.ai）",
		Icon:        "workbuddy",
		Homepage:    "https://www.codebuddy.cn",
		AccountFields: []provider.FieldDef{
			{Key: "realm", Label: "域", Kind: "badge"},
			{Key: "enterprise", Label: "企业版", Kind: "bool"},
			{Key: "checkin_done", Label: "今日签到", Kind: "bool"},
		},
	}
}

// Accounts 把池状态投影为统一账号模型。
func (p *Provider) Accounts() []account.Account {
	list := p.pool.List()
	now := time.Now()
	out := make([]account.Account, 0, len(list))
	for _, st := range list {
		out = append(out, p.toAccount(st, now))
	}
	return out
}

func (p *Provider) toAccount(st pool.Status, now time.Time) account.Account {
	a := account.Account{
		ID:       st.UID,
		Provider: ID,
		Label:    st.Nickname,
		Stats: account.Stats{
			Success:  st.SuccessCount,
			Fail:     st.ErrTotal,
			InFlight: st.InFlight,
		},
		Meta: map[string]any{
			"realm":             st.Realm,
			"enterprise":        st.Enterprise,
			"checkin_done":      st.CheckinDone,
			"consecutive_fails": st.ConsecutiveFails,
		},
	}
	if a.Label == "" {
		a.Label = shortUID(st.UID)
	}
	if !st.LastSuccessTime.IsZero() {
		t := st.LastSuccessTime
		a.Stats.LastUsedAt = &t
	}
	if st.CoolKind != "" {
		a.Meta["cool_kind"] = st.CoolKind
	}
	if len(st.RateLimitedModels) > 0 {
		a.Meta["rate_limited_models"] = st.RateLimitedModels
	}
	if len(st.ModelCosts) > 0 {
		a.Meta["model_costs"] = st.ModelCosts
	}
	if st.TokenUsage.RequestCount > 0 {
		a.Meta["token_usage"] = st.TokenUsage
	}
	if st.Enterprise {
		a.Tags = append(a.Tags, "企业版")
	}
	if st.Realm == "global" {
		a.Tags = append(a.Tags, "国际版")
	}

	// 状态：禁用 > 暂停 > 冷却 / 熔断 / 降权 > 可用
	until := latest(now, st.Until, st.BreakerUntil, st.DegradeUntil)
	switch {
	case st.Disabled:
		a.Status, a.StatusReason = account.StatusDisabled, st.DisabledReason
	case st.Paused:
		a.Status, a.StatusReason = account.StatusPaused, "运维暂停选号"
	case st.Cooling || !until.IsZero():
		a.Status, a.StatusReason = account.StatusCooling, st.Reason
		if !until.IsZero() {
			a.CoolingUntil = &until
		}
		if a.StatusReason == "" && st.BreakerUntil.After(now) {
			a.StatusReason = "连续失败熔断"
		} else if a.StatusReason == "" && st.DegradeUntil.After(now) {
			a.StatusReason = "连败降权"
		}
	default:
		a.Status = account.StatusActive
	}

	a.Quota = p.quotaOf(st)
	return a
}

func (p *Provider) quotaOf(st pool.Status) *account.Quota {
	q := &account.Quota{Unit: "credit", Remaining: float64(st.Credits), Total: float64(st.CreditsTotal)}
	if q.Total < q.Remaining {
		q.Total = q.Remaining
	}
	p.pkgMu.RLock()
	snap, ok := p.packages[st.UID]
	p.pkgMu.RUnlock()
	if ok && len(snap.items) > 0 {
		q.UpdatedAt = snap.at
		for _, it := range snap.items {
			pk := account.Package{
				ID: it.PackageCode, Name: it.Name, Source: packageSource(it),
				Amount: float64(it.Size), Remaining: float64(it.Remain),
			}
			if it.ExpiresAt > 0 {
				t := time.UnixMilli(it.ExpiresAt)
				pk.ExpireAt = &t
			}
			q.Packages = append(q.Packages, pk)
		}
		return q
	}
	// 尚未拉到明细：用池里的「最早到期批次」合成一条，保证到期视图可用。
	if !st.CreditsEarliestExpiry.IsZero() && st.CreditsEarliestRemaining > 0 {
		t := st.CreditsEarliestExpiry
		q.Packages = []account.Package{{
			Name: "最早到期批次", Source: "summary",
			Amount: float64(st.CreditsEarliestRemaining), Remaining: float64(st.CreditsEarliestRemaining),
			ExpireAt: &t,
		}}
	}
	return q
}

func packageSource(it upstream.CreditPackage) string {
	n := it.Name + it.SubProductName
	switch {
	case it.Cycle:
		return "cycle"
	case strings.Contains(n, "签到") || strings.Contains(n, "连登"):
		return "checkin"
	case strings.Contains(n, "任务") || strings.Contains(n, "成长"):
		return "task"
	case strings.Contains(n, "裂变") || strings.Contains(n, "拉新") || strings.Contains(n, "活动"):
		return "gift"
	case strings.Contains(n, "试用") || strings.Contains(strings.ToLower(n), "trial"):
		return "trial"
	}
	return "package"
}

func latest(now time.Time, ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(now) && t.After(out) {
			out = t
		}
	}
	return out
}

func shortUID(uid string) string {
	if len(uid) > 8 {
		return uid[:8]
	}
	return uid
}

// warmModelRates 启动预热倍率表，避免积分保底在首次拉目录前形同虚设（同原项目）。
func (p *Provider) warmModelRates(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if a := p.firstAccount("cn"); a != nil {
		if _, err := p.up.FetchModels(a); err != nil {
			log.Printf("WARN: [workbuddy] warm model rates (cn): %v", err)
		}
	}
	if p.up.GlobalEnabled && ctx.Err() == nil {
		if a := p.firstAccount("global"); a != nil {
			_ = p.up.FetchGlobalModelInfos(a)
		}
	}
}

func (p *Provider) firstAccount(realm string) *auth.Auth {
	uids := p.pool.AvailableUIDsForRealm(realm)
	if len(uids) == 0 {
		return nil
	}
	return p.pool.AuthByUID(uids[0])
}
