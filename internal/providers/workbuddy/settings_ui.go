package workbuddy

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/livecfg"
)

var _ provider.SettingsCapable = (*Provider)(nil)

// settings 当前生效配置（热更新后会被替换）。
func (p *Provider) settings() Settings {
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.cfg
}

func rng(min, max float64) (*float64, *float64) { return provider.Range(min, max) }

const (
	gPool     = "选号与并发"
	gCooldown = "冷却与熔断"
	gSchedule = "定时任务"
	gSession  = "会话粘性"
	gUpstream = "上游连接"
	gPrompt   = "系统提示词"
	gStorage  = "状态备份"
)

var settingsSchema = func() []provider.SettingField {
	f := func(key, label, hint, group string, kind provider.SettingKind) provider.SettingField {
		return provider.SettingField{Key: key, Label: label, Hint: hint, Group: group, Kind: kind}
	}
	withRange := func(sf provider.SettingField, min, max float64, unit string) provider.SettingField {
		sf.Min, sf.Max = rng(min, max)
		sf.Unit = unit
		return sf
	}
	restart := func(sf provider.SettingField) provider.SettingField {
		sf.Restart = true
		return sf
	}
	by := func(sf provider.SettingField, key string) provider.SettingField {
		sf.EnabledBy = key
		return sf
	}
	task := func(id, name, hint string) []provider.SettingField {
		en := "schedule." + id + "_enabled"
		return []provider.SettingField{
			f(en, name, hint, gSchedule, provider.KindBool),
			by(f("schedule."+id+"_hours", name+"时间", "每天在这些整点执行", gSchedule, provider.KindHours), en),
		}
	}
	out := []provider.SettingField{
		withRange(f("pool.max_in_flight", "单账号并发上限", "同一账号同时处理的请求数，0 表示不限", gPool, provider.KindInt), 0, 50, ""),
		withRange(f("pool.max_in_flight_global", "国际版并发上限", "国际版账号风控更严，单独限制", gPool, provider.KindInt), 1, 50, ""),
		withRange(f("pool.credit_floor", "余额保底", "余额低于该值的账号不接收费模型；积分中心设置了保底时以积分中心为准", gPool, provider.KindInt), 0, 100000, "积分"),
		f("pool.prefer_expiring", "快过期积分优先", "原算法的快过期加权（×3），与积分中心的到期优先策略叠加", gPool, provider.KindBool),
		withRange(f("pool.expiring_soon", "快过期窗口", "多久内到期算「快过期」，同时影响余额刷新时的到期统计", gPool, provider.KindDuration), 3600, 90*86400, ""),
		withRange(f("pool.cost_explore_interval", "成本探索间隔", "免费账号垄断时，隔多久把一个请求改道给未观测账号以发现限免；0 关闭", gPool, provider.KindDuration), 0, 86400, ""),
		withRange(f("pool.idle_weight_per_hour", "闲置补偿速度", "账号每闲置 1 小时增加的权重", gPool, provider.KindFloat), 0, 10, ""),
		withRange(f("pool.idle_weight_max", "闲置补偿上限", "闲置补偿最多加多少权重", gPool, provider.KindFloat), 0, 50, ""),

		withRange(f("cooldown.soft_rate", "限流冷却基数", "上游 429 后的冷却时长，连续触发按指数放大", gCooldown, provider.KindDuration), 1, 86400, ""),
		withRange(f("cooldown.soft_rate_max", "限流冷却封顶", "指数放大的上限", gCooldown, provider.KindDuration), 60, 7*86400, ""),
		withRange(f("pool.breaker_threshold", "熔断阈值", "连续失败多少次后熔断", gCooldown, provider.KindInt), 1, 100, "次"),
		withRange(f("pool.breaker_cooldown", "熔断时长", "首次熔断的冷却时长", gCooldown, provider.KindDuration), 60, 7*86400, ""),
		withRange(f("pool.breaker_cooldown_max", "熔断时长上限", "反复熔断时冷却时长的上限", gCooldown, provider.KindDuration), 60, 30*86400, ""),
		withRange(f("pool.degrade_threshold", "降权阈值", "连续失败多少次后临时降权", gCooldown, provider.KindInt), 1, 100, "次"),
		withRange(f("pool.degrade_cooldown", "降权时长", "首次降权的时长", gCooldown, provider.KindDuration), 60, 86400, ""),
		withRange(f("pool.degrade_cooldown_max", "降权时长上限", "反复降权时的上限", gCooldown, provider.KindDuration), 60, 7*86400, ""),
	}
	out = append(out, task("checkin", "每日签到", "领取签到积分")...)
	out = append(out, task("travel", "猫猫旅行", "出发与领取旅行奖励")...)
	out = append(out, task("activity", "活跃上报", "维持连续活跃天数")...)
	out = append(out, task("keepalive", "保活", "刷新登录态，防止凭证过期")...)
	out = append(out, task("blackcat", "夜猫子", "夜间活跃任务")...)
	out = append(out, task("growth", "成长任务", "扫描全部账号的成长任务并自动完成、领奖；小程序链每天零点解锁一环")...)
	out = append(out, by(withRange(f("schedule.growth_concurrency", "成长任务并发账号数", "同时执行几个账号（账号内始终串行）；部分任务含真实对话，并发越高风控风险越大", gSchedule, provider.KindInt), 1, 4, ""), "schedule.growth_enabled"))
	out = append(out,
		f("schedule.include_disabled_in_tasks", "禁用账号也执行任务", "关闭时，已禁用的账号不参与定时任务", gSchedule, provider.KindBool),
		f("schedule.balance_refresh_enabled", "余额自动刷新", "定期查询每个账号的余额", gSchedule, provider.KindBool),
		by(withRange(f("schedule.balance_refresh_minutes", "余额刷新间隔", "", gSchedule, provider.KindInt), 1, 1440, "分钟"), "schedule.balance_refresh_enabled"),
		restart(withRange(f("schedule.package_refresh_minutes", "积分包刷新间隔", "积分包明细（到期分布）的刷新周期", gSchedule, provider.KindInt), 5, 1440, "分钟")),

		restart(f("session_sticky.enabled", "会话粘性", "同一会话沿用同一账号，提高提示词缓存命中", gSession, provider.KindBool)),
		by(restart(withRange(f("session_sticky.ttl", "粘性有效期", "会话多久无请求后解绑", gSession, provider.KindDuration), 60, 7*86400, "")), "session_sticky.enabled"),

		restart(withRange(f("upstream.timeout_seconds", "请求超时", "单次上游请求的总超时", gUpstream, provider.KindInt), 10, 3600, "秒")),
		restart(withRange(f("upstream.header_timeout_seconds", "首包超时", "等待上游响应头的超时", gUpstream, provider.KindInt), 5, 3600, "秒")),
		restart(withRange(f("upstream.idle_timeout_seconds", "流式空闲超时", "流式响应多久没有新数据就断开", gUpstream, provider.KindInt), 10, 3600, "秒")),
		restart(f("global.enabled", "启用国际版", "允许添加与路由国际版账号", gUpstream, provider.KindBool)),
		f("features.sanitize_blacklist_fingerprints", "请求指纹脱敏", "去掉请求体中会被上游识别的客户端指纹", gUpstream, provider.KindBool),
		f("logging.request_client_info", "记录调用来源", "平台请求归档中记录客户端 IP 与 UA", gUpstream, provider.KindBool),

		restart(provider.SettingField{Key: "prompt.mode", Label: "提示词模式", Hint: "透传客户端的系统提示词，或替换 / 追加为自定义内容", Group: gPrompt, Kind: provider.KindSelect,
			Options: []provider.SettingOption{{Value: "passthrough", Label: "透传"}, {Value: "custom", Label: "替换"}, {Value: "append", Label: "追加"}}}),
		restart(f("prompt.file", "提示词文件", "替换或追加模式下读取的文件路径", gPrompt, provider.KindText)),

		restart(f("upstash.url", "Upstash Redis 地址", "池状态与会话粘性的云端备份；留空只用本地文件", gStorage, provider.KindText)),
		restart(f("upstash.token", "Upstash Token", "", gStorage, provider.KindSecret)),
	)
	return out
}()

// SettingsSchema 实现 provider.SettingsCapable。
func (p *Provider) SettingsSchema() []provider.SettingField { return settingsSchema }

// SettingsRaw 实现 provider.SettingsCapable：返回已保存的配置（需重启的字段可能尚未生效）。
func (p *Provider) SettingsRaw() json.RawMessage {
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	b, _ := json.Marshal(p.desired)
	return b
}

func restartKeys() []string {
	var keys []string
	for _, f := range settingsSchema {
		if f.Restart {
			keys = append(keys, f.Key)
		}
	}
	return keys
}

// PendingRestart 实现 provider.SettingsCapable。
func (p *Provider) PendingRestart() []string {
	p.cfgMu.RLock()
	a, _ := json.Marshal(p.started)
	b, _ := json.Marshal(p.desired)
	p.cfgMu.RUnlock()
	return provider.DiffKeys(a, b, restartKeys())
}

// ApplySettings 实现 provider.SettingsCapable：校验 → 热应用 → 返回待重启项。
func (p *Provider) ApplySettings(raw json.RawMessage) ([]string, error) {
	p.cfgMu.RLock()
	dataDir := p.cfg.dataDir
	p.cfgMu.RUnlock()
	n, err := ParseSettings(raw, dataDir)
	if err != nil {
		return nil, err
	}
	if n.Cooldown.SoftRate != "" && n.softRateMax < n.softRate {
		return nil, fmt.Errorf("限流冷却封顶不能小于冷却基数")
	}
	if n.breakerCooldownMax < n.breakerCooldown {
		return nil, fmt.Errorf("熔断时长上限不能小于熔断时长")
	}
	if n.degradeCooldownMax < n.degradeCooldown {
		return nil, fmt.Errorf("降权时长上限不能小于降权时长")
	}
	// 需重启的字段保持运行值：只替换「运行配置」中可热更新的部分，
	// 避免依赖这些字段的代码（如登录时读 global.enabled）提前看到未生效的值。
	p.cfgMu.Lock()
	run := n
	run.SessionSticky = p.cfg.SessionSticky
	run.sessionTTL, run.sessionGC = p.cfg.sessionTTL, p.cfg.sessionGC
	run.Upstream = p.cfg.Upstream
	run.Global = p.cfg.Global
	run.Prompt, run.promptText = p.cfg.Prompt, p.cfg.promptText
	run.Upstash = p.cfg.Upstash
	run.Schedule.PackageRefreshMinutes, run.packageRefresh = p.cfg.Schedule.PackageRefreshMinutes, p.cfg.packageRefresh
	p.cfg = run
	p.desired = n
	reserve := p.reserve
	p.cfgMu.Unlock()

	pl := p.pool
	pl.SetMaxInFlight(n.Pool.MaxInFlight)
	pl.SetMaxInFlightGlobal(n.Pool.MaxInFlightGlobal)
	pl.SetBreaker(n.Pool.BreakerThreshold, n.breakerCooldown, n.breakerCooldownMax)
	pl.SetDegrade(n.Pool.DegradeThreshold, n.degradeCooldown, n.degradeCooldownMax)
	pl.SetSoftRateMax(n.softRateMax)
	pl.SetCostExploreInterval(n.costExploreInterval)
	pl.SetWeights(n.Pool.IdleWeightPerHour, n.Pool.IdleWeightMax)
	pl.SetPreferExpiring(n.Pool.PreferExpiring)
	if reserve <= 0 {
		pl.SetCreditFloor(n.Pool.CreditFloor)
	}
	p.live.Store(livecfg.Snapshot{
		SoftCooldown:         n.softRate,
		SanitizeFingerprints: n.Features.SanitizeBlacklistFingerprints,
		RecordClientInfo:     n.Logging.RequestClientInfo,
	})
	p.up.SanitizeFingerprints.Store(n.Features.SanitizeBlacklistFingerprints)

	s := n.Schedule
	p.sched.Reconfigure(s.CheckinHours, s.TravelHours, s.ActivityHours, s.KeepaliveHours, s.BlackcatHours, s.GrowthHours,
		!s.CheckinEnabled, !s.TravelEnabled, !s.ActivityEnabled, !s.KeepaliveEnabled, !s.BlackcatEnabled, !s.GrowthEnabled)
	p.sched.SetIncludeDisabledInTasks(s.IncludeDisabledInTasks)
	p.sched.SetExpiringSoonWindow(n.expiringSoon)
	var bal time.Duration
	if s.BalanceRefreshEnabled {
		bal = n.balanceRefresh
	}
	p.sched.SetBalanceInterval(bal)

	// 待重启项按「启动配置 vs 已保存配置」比较（运行配置里这些字段没变）。
	return p.PendingRestart(), nil
}
