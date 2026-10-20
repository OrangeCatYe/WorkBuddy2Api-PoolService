package update

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/orangeruan/poolservice/internal/config"
)

// BuiltinID 内置官方下载源的 ID，不允许被自定义源占用。
const BuiltinID = "github"

const (
	repoBase   = "https://github.com/OrangeCatYe/WorkBuddy2Api-PoolService/releases"
	maxSources = 8
)

// Source 一个下载源：清单地址 + 安装包地址模板。
type Source struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ManifestURL string `json:"manifest_url"`
	AssetURL    string `json:"asset_url"`
	Builtin     bool   `json:"builtin,omitempty"`
}

// Builtin 官方 GitHub 源。使用 releases/latest/download 直链，不经过 GitHub API，
// 因此不占匿名 API 的每小时 60 次额度。
func Builtin() Source {
	return Source{
		ID: BuiltinID, Name: "GitHub（官方）", Builtin: true,
		ManifestURL: repoBase + "/latest/download/latest.json",
		AssetURL:    repoBase + "/download/{tag}/{name}",
	}
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidateURL 只接受 https；回环地址允许 http（本机调试 / 测试用）。不允许带账号密码。
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("地址无效: %q", raw)
	}
	if u.User != nil {
		return errors.New("地址中不能包含账号密码")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return errors.New("只支持 https 地址（本机回环地址除外）")
		}
	default:
		return errors.New("只支持 https 地址")
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateSource 校验一个自定义源。
func ValidateSource(s config.UpdateSource) error {
	if !idPattern.MatchString(s.ID) {
		return fmt.Errorf("下载源 ID %q 无效：只能用小写字母、数字、- 和 _，最长 32 位", s.ID)
	}
	if s.ID == BuiltinID {
		return fmt.Errorf("下载源 ID %q 为内置保留", BuiltinID)
	}
	if n := strings.TrimSpace(s.Name); n == "" || len([]rune(n)) > 40 {
		return fmt.Errorf("下载源 %q 的名称不能为空且不超过 40 个字", s.ID)
	}
	if err := ValidateURL(s.ManifestURL); err != nil {
		return fmt.Errorf("下载源 %q 的清单地址：%w", s.ID, err)
	}
	if !strings.Contains(s.AssetURL, "{name}") {
		return fmt.Errorf("下载源 %q 的安装包地址必须包含 {name}", s.ID)
	}
	// 用样例值展开后再校验，保证模板本身产出合法地址。
	if err := ValidateURL(expand(s.AssetURL, "v0.0.0", "x.zip")); err != nil {
		return fmt.Errorf("下载源 %q 的安装包地址：%w", s.ID, err)
	}
	return nil
}

// ValidateSources 校验整个自定义源列表（数量、重复、逐项合法）。
func ValidateSources(list []config.UpdateSource) error {
	if len(list) > maxSources {
		return fmt.Errorf("自定义下载源最多 %d 个", maxSources)
	}
	seen := map[string]bool{}
	for _, s := range list {
		if err := ValidateSource(s); err != nil {
			return err
		}
		if seen[s.ID] {
			return fmt.Errorf("下载源 ID %q 重复", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

// Sources 内置源加上所有合法的自定义源。配置里不合法的自定义源会被跳过而不是让整个功能失效。
func Sources(cfg config.UpdateConfig) []Source {
	out := []Source{Builtin()}
	for _, c := range cfg.Sources {
		if ValidateSource(c) != nil {
			continue
		}
		out = append(out, Source{ID: c.ID, Name: strings.TrimSpace(c.Name), ManifestURL: c.ManifestURL, AssetURL: c.AssetURL})
	}
	return out
}

// Find 按 ID 取源；id 为空时取配置里选中的源。
func Find(cfg config.UpdateConfig, id string) (Source, error) {
	if id == "" {
		id = cfg.Source
	}
	for _, s := range Sources(cfg) {
		if s.ID == id {
			return s, nil
		}
	}
	return Source{}, fmt.Errorf("未知的下载源 %q", id)
}

// SigURL 签名文件地址：清单地址的路径后加 .sig（保留查询参数）。
func (s Source) SigURL() string {
	u, err := url.Parse(s.ManifestURL)
	if err != nil {
		return s.ManifestURL + ".sig"
	}
	u.Path += ".sig"
	return u.String()
}

// AssetFor 展开安装包地址模板。tag 和 name 都已经过清单校验（tag 形如 v1.2.3，name 是单层文件名）。
func (s Source) AssetFor(tag, name string) string { return expand(s.AssetURL, tag, name) }

func expand(tpl, tag, name string) string {
	return strings.NewReplacer(
		"{tag}", tag,
		"{version}", strings.TrimPrefix(tag, "v"),
		"{name}", url.PathEscape(name),
	).Replace(tpl)
}
