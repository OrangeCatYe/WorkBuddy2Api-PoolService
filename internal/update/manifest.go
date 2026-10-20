package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

const (
	manifestSchema   = 1
	maxManifestBytes = 1 << 20
	maxSigBytes      = 4 << 10
	maxNotesBytes    = 16 << 10
	// MaxAssetBytes 安装包大小上限。当前约 6 MB，留足余量防止异常清单诱导下载超大文件。
	MaxAssetBytes = 200 << 20
)

// ErrUnsupportedSchema 清单版本比本程序认识的更新，需要先手动升级到支持的版本。
var ErrUnsupportedSchema = errors.New("更新清单格式比当前程序新，请先手动下载最新版安装")

// Asset 一个平台的安装包。
type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest Release 里的 latest.json。
type Manifest struct {
	Schema      int              `json:"schema"`
	Version     string           `json:"version"`
	Tag         string           `json:"tag"`
	PublishedAt string           `json:"published_at,omitempty"`
	Notes       string           `json:"notes,omitempty"`
	Assets      map[string]Asset `json:"assets"`
}

// PlatformKey 当前平台在 Assets 里的键，如 windows-amd64。
func PlatformKey() string { return runtime.GOOS + "-" + runtime.GOARCH }

// ParseManifest 解析并校验清单。不做签名校验，签名由调用方另行验证。
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes {
		return nil, errors.New("更新清单为空或过大")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("更新清单解析失败: %w", err)
	}
	if m.Schema > manifestSchema {
		return nil, ErrUnsupportedSchema
	}
	if m.Schema != manifestSchema {
		return nil, fmt.Errorf("不支持的更新清单格式 %d", m.Schema)
	}
	v, ok := ParseVersion(m.Version)
	if !ok || m.Version != v.String() {
		return nil, fmt.Errorf("更新清单版本号无效: %q", m.Version)
	}
	if m.Tag != v.Tag() {
		return nil, fmt.Errorf("更新清单 tag %q 与版本 %q 不一致", m.Tag, m.Version)
	}
	if len(m.Notes) > maxNotesBytes {
		m.Notes = m.Notes[:maxNotesBytes]
	}
	for key, a := range m.Assets {
		if !validAssetName(a.Name) {
			return nil, fmt.Errorf("更新清单中 %s 的文件名无效: %q", key, a.Name)
		}
		if a.Size <= 0 || a.Size > MaxAssetBytes {
			return nil, fmt.Errorf("更新清单中 %s 的文件大小无效: %d", key, a.Size)
		}
		if !validSHA256(a.SHA256) {
			return nil, fmt.Errorf("更新清单中 %s 的 sha256 无效", key)
		}
	}
	return &m, nil
}

// validAssetName 只允许单层、无特殊字符的文件名，防止清单把安装包写到别处或拼出奇怪的 URL。
func validAssetName(n string) bool {
	if n == "" || len(n) > 100 || n == "." || n == ".." {
		return false
	}
	for _, c := range n {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

// ParseKeys 解码 base64 公钥列表。
func ParseKeys(b64 []string) ([]ed25519.PublicKey, error) {
	out := make([]ed25519.PublicKey, 0, len(b64))
	for _, s := range b64 {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("无效的公钥: %q", s)
		}
		out = append(out, ed25519.PublicKey(b))
	}
	return out, nil
}

// VerifySignature 校验 sig（base64 文本）是否为 raw 的有效 ed25519 签名，任意一把公钥通过即可
// （支持密钥轮换：新旧公钥可同时受信）。
func VerifySignature(raw, sig []byte, keys []ed25519.PublicKey) bool {
	if len(sig) == 0 || len(sig) > maxSigBytes {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(b) != ed25519.SignatureSize {
		return false
	}
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, raw, b) {
			return true
		}
	}
	return false
}

// Sign 用私钥对 raw 签名，返回 base64 文本（发布工具使用）。
func Sign(priv ed25519.PrivateKey, raw []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw)) + "\n")
}
