package workbuddy

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/prompt"
)

// Settings WorkBuddy 适配器配置（config.json 的 providers.workbuddy 段）。
//
// 字段与原 workbuddy2api-panel 的 config.json 同名同义，旧配置可直接把对应段落
// 搬进来；listen / api_key / panel 等进程级字段已上移到 PoolService 顶层配置。
type Settings struct {
	Enabled bool `json:"enabled"`

	// AuthDir 凭证目录；空 = <data_dir>/workbuddy/auths。
	// 迁移旧项目时可直接指向旧项目的 auths 目录。
	AuthDir string `json:"auth_dir"`
	// StateFile 池状态文件；空 = <data_dir>/workbuddy/state.json。
	StateFile string `json:"state_file"`

	Logging struct {
		RequestArchiveEnabled bool `json:"request_archive_enabled"`
		RequestRetentionDays  int  `json:"request_retention_days"`
		RequestArchiveMaxMB   int  `json:"request_archive_max_mb"`
		RequestClientInfo     bool `json:"request_client_info"`
	} `json:"logging"`

	Cooldown struct {
		SoftRate    string `json:"soft_rate"`
		SoftRateMax string `json:"soft_rate_max"`
	} `json:"cooldown"`

	Schedule struct {
		CheckinHours           []int `json:"checkin_hours"`
		TravelHours            []int `json:"travel_hours"`
		ActivityHours          []int `json:"activity_hours"`
		KeepaliveHours         []int `json:"keepalive_hours"`
		BlackcatHours          []int `json:"blackcat_hours"`
		GrowthHours            []int `json:"growth_hours"`
		CheckinEnabled         bool  `json:"checkin_enabled"`
		TravelEnabled          bool  `json:"travel_enabled"`
		ActivityEnabled        bool  `json:"activity_enabled"`
		KeepaliveEnabled       bool  `json:"keepalive_enabled"`
		BlackcatEnabled        bool  `json:"blackcat_enabled"`
		GrowthEnabled          bool  `json:"growth_enabled"`
		IncludeDisabledInTasks bool  `json:"include_disabled_in_tasks"`
		BalanceRefreshEnabled  bool  `json:"balance_refresh_enabled"`
		BalanceRefreshMinutes  int   `json:"balance_refresh_minutes"`
		// PackageRefreshMinutes 积分包明细（到期分布）刷新周期，缺省 30。
		PackageRefreshMinutes int `json:"package_refresh_minutes"`
	} `json:"schedule"`

	Global struct {
		Enabled     bool   `json:"enabled"`
		ChatBase    string `json:"chat_base"`
		BillingBase string `json:"billing_base"`
	} `json:"global"`

	Upstream struct {
		TimeoutSeconds       int    `json:"timeout_seconds"`
		HeaderTimeoutSeconds int    `json:"header_timeout_seconds"`
		IdleTimeoutSeconds   int    `json:"idle_timeout_seconds"`
		UserAgent            string `json:"user_agent"`
		ClientVersion        string `json:"client_version"`
		CliVersion           string `json:"cli_version"`
		ClientName           string `json:"client_name"`
		DeviceToken          string `json:"device_token"`
		DeviceTokenFile      string `json:"device_token_file"`
		PassthroughIP        bool   `json:"passthrough_ip"`
	} `json:"upstream"`

	Features struct {
		SanitizeBlacklistFingerprints bool `json:"sanitize_blacklist_fingerprints"`
	} `json:"features"`

	Prompt struct {
		Mode string `json:"mode"` // passthrough / custom / append
		File string `json:"file"`
	} `json:"prompt"`

	Upstash struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"upstash"`

	Pool struct {
		MaxInFlight         int     `json:"max_in_flight"`
		MaxInFlightGlobal   int     `json:"max_in_flight_global"`
		BreakerThreshold    int     `json:"breaker_threshold"`
		BreakerCooldown     string  `json:"breaker_cooldown"`
		BreakerCooldownMax  string  `json:"breaker_cooldown_max"`
		DegradeThreshold    int     `json:"degrade_threshold"`
		DegradeCooldown     string  `json:"degrade_cooldown"`
		DegradeCooldownMax  string  `json:"degrade_cooldown_max"`
		IdleWeightPerHour   float64 `json:"idle_weight_per_hour"`
		IdleWeightMax       float64 `json:"idle_weight_max"`
		PreferExpiring      bool    `json:"prefer_expiring"`
		ExpiringSoon        string  `json:"expiring_soon"`
		CostExploreInterval string  `json:"cost_explore_interval"`
		CreditFloor         int64   `json:"credit_floor"`
	} `json:"pool"`

	SessionSticky struct {
		Enabled    bool   `json:"enabled"`
		TTL        string `json:"ttl"`
		GCInterval string `json:"gc_interval"`
	} `json:"session_sticky"`

	// 解析结果
	promptText          string
	softRate            time.Duration
	softRateMax         time.Duration
	breakerCooldown     time.Duration
	breakerCooldownMax  time.Duration
	degradeCooldown     time.Duration
	degradeCooldownMax  time.Duration
	sessionTTL          time.Duration
	sessionGC           time.Duration
	balanceRefresh      time.Duration
	packageRefresh      time.Duration
	expiringSoon        time.Duration
	costExploreInterval time.Duration
	dataDir             string
}

// DefaultSettings 返回与原项目一致的推荐默认值。
func DefaultSettings() Settings {
	var c Settings
	c.Enabled = true
	c.Cooldown.SoftRate = "600s"
	c.Cooldown.SoftRateMax = "2h"
	c.Logging.RequestArchiveEnabled = true
	c.Logging.RequestRetentionDays = 7
	c.Logging.RequestArchiveMaxMB = 100
	c.Logging.RequestClientInfo = true
	c.Schedule.CheckinHours = []int{9, 21}
	c.Schedule.TravelHours = []int{9, 21}
	c.Schedule.ActivityHours = []int{10}
	c.Schedule.KeepaliveHours = []int{22}
	c.Schedule.BlackcatHours = []int{23}
	c.Schedule.GrowthHours = []int{1}
	c.Schedule.CheckinEnabled = true
	c.Schedule.GrowthEnabled = true
	c.Schedule.TravelEnabled = true
	c.Schedule.ActivityEnabled = true
	c.Schedule.KeepaliveEnabled = true
	c.Schedule.BlackcatEnabled = true
	c.Schedule.BalanceRefreshEnabled = true
	c.Schedule.BalanceRefreshMinutes = 5
	c.Schedule.PackageRefreshMinutes = 30
	c.Upstream.TimeoutSeconds = 120
	c.Global.Enabled = true
	c.Features.SanitizeBlacklistFingerprints = true
	c.Prompt.Mode = "passthrough"
	c.Pool.MaxInFlight = 3
	c.Pool.MaxInFlightGlobal = 2
	c.Pool.BreakerThreshold = 3
	c.Pool.BreakerCooldown = "30m"
	c.Pool.BreakerCooldownMax = "6h"
	c.Pool.DegradeThreshold = 5
	c.Pool.DegradeCooldown = "10m"
	c.Pool.DegradeCooldownMax = "2h"
	c.Pool.IdleWeightPerHour = 0.5
	c.Pool.IdleWeightMax = 5.0
	c.Pool.PreferExpiring = true
	c.Pool.ExpiringSoon = "168h"
	c.Pool.CostExploreInterval = "30m"
	c.SessionSticky.Enabled = true
	c.SessionSticky.TTL = "30m"
	c.SessionSticky.GCInterval = "5m"
	return c
}

// ParseSettings 在默认值上叠加 raw（可为空）并校验。dataDir 用于推导默认路径。
func ParseSettings(raw json.RawMessage, dataDir string) (Settings, error) {
	s := DefaultSettings()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return s, fmt.Errorf("providers.workbuddy: %w", err)
		}
	}
	s.dataDir = dataDir
	return s, s.normalize()
}

func (c *Settings) dir(name string) string {
	return filepath.Join(c.dataDir, "workbuddy", name)
}

// stateSibling 与 state 文件同目录的数据文件（与原项目同规则）。
func (c *Settings) stateSibling(name string) string {
	return filepath.Join(filepath.Dir(c.StateFile), name)
}

func parseDur(field, v, def string, allowZero bool) (time.Duration, error) {
	if strings.TrimSpace(v) == "" {
		v = def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("providers.workbuddy.%s: %w", field, err)
	}
	if d < 0 || (!allowZero && d == 0) {
		if allowZero {
			return 0, nil
		}
		return time.ParseDuration(def)
	}
	return d, nil
}

func (c *Settings) normalize() error {
	if c.AuthDir == "" {
		c.AuthDir = c.dir("auths")
	}
	if c.StateFile == "" {
		c.StateFile = c.dir("state.json")
	}
	if c.Logging.RequestRetentionDays <= 0 {
		c.Logging.RequestRetentionDays = 7
	}
	if c.Logging.RequestArchiveMaxMB <= 0 {
		c.Logging.RequestArchiveMaxMB = 100
	}
	var err error
	steps := []struct {
		dst       *time.Duration
		field     string
		val, def  string
		allowZero bool
	}{
		{&c.softRate, "cooldown.soft_rate", c.Cooldown.SoftRate, "600s", false},
		{&c.softRateMax, "cooldown.soft_rate_max", c.Cooldown.SoftRateMax, "2h", false},
		{&c.breakerCooldown, "pool.breaker_cooldown", c.Pool.BreakerCooldown, "30m", false},
		{&c.breakerCooldownMax, "pool.breaker_cooldown_max", c.Pool.BreakerCooldownMax, "6h", false},
		{&c.degradeCooldown, "pool.degrade_cooldown", c.Pool.DegradeCooldown, "10m", false},
		{&c.degradeCooldownMax, "pool.degrade_cooldown_max", c.Pool.DegradeCooldownMax, "2h", false},
		{&c.sessionTTL, "session_sticky.ttl", c.SessionSticky.TTL, "30m", false},
		{&c.sessionGC, "session_sticky.gc_interval", c.SessionSticky.GCInterval, "5m", false},
		{&c.costExploreInterval, "pool.cost_explore_interval", c.Pool.CostExploreInterval, "30m", true},
	}
	for _, st := range steps {
		if *st.dst, err = parseDur(st.field, st.val, st.def, st.allowZero); err != nil {
			return err
		}
	}
	if c.Pool.ExpiringSoon != "" {
		if c.expiringSoon, err = parseDur("pool.expiring_soon", c.Pool.ExpiringSoon, "0", true); err != nil {
			return err
		}
	}
	if c.Pool.CreditFloor < 0 {
		c.Pool.CreditFloor = 0
	}
	if c.Pool.BreakerThreshold <= 0 {
		c.Pool.BreakerThreshold = 3
	}
	if c.Pool.DegradeThreshold <= 0 {
		c.Pool.DegradeThreshold = 5
	}
	if c.Pool.MaxInFlightGlobal <= 0 {
		c.Pool.MaxInFlightGlobal = 2
	}
	if c.Pool.IdleWeightPerHour <= 0 {
		c.Pool.IdleWeightPerHour = 0.5
	}
	if c.Pool.IdleWeightMax <= 0 {
		c.Pool.IdleWeightMax = 5.0
	}
	if c.Upstream.TimeoutSeconds <= 0 {
		c.Upstream.TimeoutSeconds = 120
	}
	if c.Upstream.HeaderTimeoutSeconds <= 0 {
		c.Upstream.HeaderTimeoutSeconds = c.Upstream.TimeoutSeconds
	}
	if c.Upstream.IdleTimeoutSeconds <= 0 {
		c.Upstream.IdleTimeoutSeconds = 300
	}
	def := DefaultSettings().Schedule
	fill := func(dst *[]int, d []int) {
		if len(*dst) == 0 {
			*dst = d
		}
	}
	fill(&c.Schedule.CheckinHours, def.CheckinHours)
	fill(&c.Schedule.TravelHours, def.TravelHours)
	fill(&c.Schedule.ActivityHours, def.ActivityHours)
	fill(&c.Schedule.KeepaliveHours, def.KeepaliveHours)
	fill(&c.Schedule.BlackcatHours, def.BlackcatHours)
	fill(&c.Schedule.GrowthHours, def.GrowthHours)
	for name, hours := range map[string][]int{
		"checkin_hours": c.Schedule.CheckinHours, "travel_hours": c.Schedule.TravelHours,
		"activity_hours": c.Schedule.ActivityHours, "keepalive_hours": c.Schedule.KeepaliveHours,
		"blackcat_hours": c.Schedule.BlackcatHours, "growth_hours": c.Schedule.GrowthHours,
	} {
		for _, h := range hours {
			if h < 0 || h > 23 {
				return fmt.Errorf("providers.workbuddy.schedule.%s: %d 不是合法小时（0-23）", name, h)
			}
		}
	}
	if c.Schedule.BalanceRefreshEnabled {
		if c.Schedule.BalanceRefreshMinutes <= 0 {
			c.Schedule.BalanceRefreshMinutes = 5
		}
		c.balanceRefresh = time.Duration(c.Schedule.BalanceRefreshMinutes) * time.Minute
	}
	if c.Schedule.PackageRefreshMinutes <= 0 {
		c.Schedule.PackageRefreshMinutes = 30
	}
	c.packageRefresh = time.Duration(c.Schedule.PackageRefreshMinutes) * time.Minute

	switch m := strings.ToLower(strings.TrimSpace(c.Prompt.Mode)); m {
	case "", "passthrough":
		c.Prompt.Mode = "passthrough"
	case "custom", "append":
		c.Prompt.Mode = m
		text, err := prompt.Load(m, c.Prompt.File)
		if err != nil {
			return err
		}
		c.promptText = text
	default:
		return fmt.Errorf("providers.workbuddy.prompt.mode: %q 不是合法值（passthrough / custom / append）", c.Prompt.Mode)
	}
	return nil
}
