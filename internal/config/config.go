// Package config 服务配置：JSON 文件 + 环境变量覆盖，原子写回。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Duration 支持 "30s" / "10m" 字符串的时长。
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		d.Duration = v
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("invalid duration %s", string(b))
	}
	d.Duration = time.Duration(n * float64(time.Second))
	return nil
}

// Config 顶层配置。
type Config struct {
	Listen   string `json:"listen"`
	DataDir  string `json:"data_dir"`
	AdminKey string `json:"admin_key"` // 面板与管理接口密钥；空 = 不鉴权（仅限本机）

	Gateway GatewayConfig `json:"gateway"`

	// Credits 积分预测与选号策略。
	Credits CreditsConfig `json:"credits"`

	// Update 软件更新：下载源、代理、自动检查。
	Update UpdateConfig `json:"update"`

	// Providers 各 Provider 的私有配置，原样交给适配器解析。
	Providers map[string]json.RawMessage `json:"providers,omitempty"`

	path string
}

// UpdateConfig 软件更新配置。全部字段可选，缺省值见 normalize。
type UpdateConfig struct {
	// Source 当前使用的下载源 ID；内置 "github"，其余来自 Sources。
	Source string `json:"source"`
	// Sources 自定义下载源（镜像、Gitee、自建），清单里只写文件名，URL 由模板拼出。
	Sources []UpdateSource `json:"sources,omitempty"`
	// Proxy 访问下载源使用的代理（http / https / socks5）；空 = 跟随系统环境变量。
	Proxy string `json:"proxy,omitempty"`
	// AutoCheck 启动后和定期自动检查更新（只检查，不下载不安装）。默认关闭。
	AutoCheck bool `json:"auto_check"`
	// CheckInterval 自动检查间隔，最短 1 小时，默认 24 小时。
	CheckInterval Duration `json:"check_interval"`
}

// UpdateSource 一个自定义下载源。
type UpdateSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ManifestURL string `json:"manifest_url"` // latest.json 的地址，签名文件为其后加 .sig
	AssetURL    string `json:"asset_url"`    // 安装包地址模板，可用 {tag} {version} {name}
}

// GatewayConfig 网关配置。
type GatewayConfig struct {
	// ModelNaming /v1/models 输出风格：short（短名，默认）/ full（provider/model）/ both
	ModelNaming string   `json:"model_naming"`
	CatalogTTL  Duration `json:"catalog_ttl"`
	// CreditInModelName 模型 ID / 显示名末尾附加积分倍率，如 glm-5.2(x0.05)。
	// 带后缀和不带后缀的名字都能请求；倍率变化后旧名仍可解析。
	CreditInModelName bool `json:"credit_in_model_name"`
	// LegacyAPIKey 兼容旧版单密钥；P3 起推荐改用下游 API Key 管理
	LegacyAPIKey string `json:"legacy_api_key,omitempty"`
	// AllowAnonymous 未配置任何下游 Key 时是否放行（默认 false：必须带 Key）
	AllowAnonymous bool `json:"allow_anonymous"`
}

// CreditsConfig 积分策略。
type CreditsConfig struct {
	// Strategy 选号策略：waste_first（默认，快过期 / 将浪费的积分优先）或 off（沿用平台原算法）。
	Strategy string `json:"strategy"`
	// WasteBoost 紧迫度最高的账号权重放大倍数（1~10，默认 4）。
	WasteBoost float64 `json:"waste_boost"`
	// ExpensiveRate 倍率 ≥ 该值的模型视为高价模型，进一步集中到将浪费积分的账号（默认 0.5）。
	ExpensiveRate float64 `json:"expensive_rate"`
	// DailyCap 单账号每日积分软上限；超过后降权到 0.05（仍可兜底）。0 = 不限。
	DailyCap float64 `json:"daily_cap"`
	// Reserve 余额保底：低于该值的账号不接收费模型；0 = 沿用平台自身配置。
	Reserve float64 `json:"reserve"`
	// BurnWindowDays 消耗速度统计窗口（天，1~30，默认 7）。
	BurnWindowDays int `json:"burn_window_days"`
}

// Default 返回默认配置。
func Default() *Config {
	return &Config{
		Listen:  "127.0.0.1:7863",
		DataDir: "./data",
		Gateway: GatewayConfig{
			ModelNaming: "short",
			CatalogTTL:  Duration{10 * time.Minute},
		},
		Credits:   CreditsConfig{Strategy: "waste_first", WasteBoost: 4, ExpensiveRate: 0.5, BurnWindowDays: 7},
		Update:    UpdateConfig{Source: "github", CheckInterval: Duration{24 * time.Hour}},
		Providers: map[string]json.RawMessage{},
	}
}

// Path 配置文件路径。
func (c *Config) Path() string { return c.path }

// Clone 深拷贝（Providers 原始 JSON 段、更新源列表逐个复制）。
func (c *Config) Clone() *Config {
	n := *c
	n.Providers = make(map[string]json.RawMessage, len(c.Providers))
	for k, v := range c.Providers {
		n.Providers[k] = append(json.RawMessage(nil), v...)
	}
	n.Update.Sources = append([]UpdateSource(nil), c.Update.Sources...)
	return &n
}

// Load 读取配置；文件不存在时生成默认配置（含随机管理密钥）并落盘。
func Load(path string) (*Config, bool, error) {
	cfg := Default()
	cfg.path = path
	created := false
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		cfg.AdminKey = RandomKey("adm-")
		if err := cfg.Save(); err != nil {
			return nil, false, err
		}
		created = true
	default:
		return nil, false, err
	}
	cfg.applyEnv()
	cfg.normalize()
	return cfg, created, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("POOLSVC_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("POOLSVC_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("POOLSVC_ADMIN_KEY"); v != "" {
		c.AdminKey = v
	}
}

func (c *Config) normalize() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:7863"
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	switch strings.ToLower(c.Gateway.ModelNaming) {
	case "short", "full", "both":
		c.Gateway.ModelNaming = strings.ToLower(c.Gateway.ModelNaming)
	default:
		c.Gateway.ModelNaming = "short"
	}
	if c.Gateway.CatalogTTL.Duration <= 0 {
		c.Gateway.CatalogTTL = Duration{10 * time.Minute}
	}
	if c.Providers == nil {
		c.Providers = map[string]json.RawMessage{}
	}
	c.Credits.Normalize()
	c.Update.Normalize()
}

// MinCheckInterval 自动检查更新的最短间隔。
const MinCheckInterval = time.Hour

// Normalize 补全缺省值，修正非法值。
func (u *UpdateConfig) Normalize() {
	u.Source = strings.TrimSpace(u.Source)
	if u.Source == "" {
		u.Source = "github"
	}
	u.Proxy = strings.TrimSpace(u.Proxy)
	switch {
	case u.CheckInterval.Duration <= 0:
		u.CheckInterval = Duration{24 * time.Hour}
	case u.CheckInterval.Duration < MinCheckInterval:
		u.CheckInterval = Duration{MinCheckInterval}
	}
}

// ProviderConfig 把某 Provider 的配置解码到 out；未配置时保持 out 默认值。
func (c *Config) ProviderConfig(id string, out any) error {
	raw, ok := c.Providers[id]
	if !ok || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Save 原子写回（tmp + rename，权限 0600）。
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config path is empty")
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(c.path, append(b, '\n'), 0o600)
}

// WriteFileAtomic 原子写文件。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	_ = os.Chmod(name, perm)
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// RandomKey 生成带前缀的 32 字节随机密钥。
func RandomKey(prefix string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

// Normalize 修正非法值。
func (c *CreditsConfig) Normalize() {
	switch c.Strategy {
	case "waste_first", "off":
	default:
		c.Strategy = "waste_first"
	}
	if c.WasteBoost < 1 {
		c.WasteBoost = 4
	}
	if c.WasteBoost > 10 {
		c.WasteBoost = 10
	}
	if c.ExpensiveRate <= 0 {
		c.ExpensiveRate = 0.5
	}
	if c.DailyCap < 0 {
		c.DailyCap = 0
	}
	if c.Reserve < 0 {
		c.Reserve = 0
	}
	if c.BurnWindowDays <= 0 {
		c.BurnWindowDays = 7
	}
	if c.BurnWindowDays > 30 {
		c.BurnWindowDays = 30
	}
}
