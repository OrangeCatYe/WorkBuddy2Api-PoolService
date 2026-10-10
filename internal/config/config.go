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

	// Providers 各 Provider 的私有配置，原样交给适配器解析。
	Providers map[string]json.RawMessage `json:"providers,omitempty"`

	path string
}

// GatewayConfig 网关配置。
type GatewayConfig struct {
	// ModelNaming /v1/models 输出风格：short（短名，默认）/ full（provider/model）/ both
	ModelNaming string   `json:"model_naming"`
	CatalogTTL  Duration `json:"catalog_ttl"`
	// LegacyAPIKey 兼容旧版单密钥；P3 起推荐改用下游 API Key 管理
	LegacyAPIKey string `json:"legacy_api_key,omitempty"`
	// AllowAnonymous 未配置任何下游 Key 时是否放行（默认 false：必须带 Key）
	AllowAnonymous bool `json:"allow_anonymous"`
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
		Providers: map[string]json.RawMessage{},
	}
}

// Path 配置文件路径。
func (c *Config) Path() string { return c.path }

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
