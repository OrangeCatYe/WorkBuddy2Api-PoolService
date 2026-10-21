package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var assetPattern = regexp.MustCompile(`^poolsvc-(windows|linux|darwin)-(amd64|arm64)\.(zip|tar\.gz)$`)

// BuildManifest 根据发布产物目录生成 latest.json（发布工具使用）。
// dir 下的安装包文件名必须形如 poolsvc-<os>-<arch>.zip / .tar.gz。
func BuildManifest(dir, tag, changelog string, publishedAt time.Time) ([]byte, error) {
	v, ok := ParseVersion(tag)
	if !ok || tag != v.Tag() {
		return nil, fmt.Errorf("标签 %q 不是 vX.Y.Z 形式的正式版本", tag)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	m := Manifest{
		Schema: manifestSchema, Version: v.String(), Tag: tag,
		PublishedAt: publishedAt.UTC().Format(time.RFC3339),
		Notes:       ReleaseNotes(changelog, v.String()),
		Assets:      map[string]Asset{},
	}
	for _, e := range ents {
		sm := assetPattern.FindStringSubmatch(e.Name())
		if e.IsDir() || sm == nil {
			continue
		}
		sum, size, err := hashFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		m.Assets[sm[1]+"-"+sm[2]] = Asset{Name: e.Name(), Size: size, SHA256: sum}
	}
	if len(m.Assets) == 0 {
		return nil, fmt.Errorf("%s 下没有找到 poolsvc-<os>-<arch>.zip/.tar.gz 安装包", dir)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if _, err := ParseManifest(b); err != nil { // 生成出来的清单必须能通过客户端的同一套校验
		return nil, fmt.Errorf("生成的清单未通过校验: %w", err)
	}
	return b, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ReleaseNotes 取变更记录里 `## [version]` 小节的正文（不含标题行），找不到返回空串。
// 超过 maxNotesBytes 的部分会被截掉：更新清单要保持精简，客户端本来也只展示这一段。
func ReleaseNotes(changelog, version string) string {
	return truncateUTF8(releaseSection(changelog, version), maxNotesBytes)
}

// releaseSection 取 `## [version]` 小节的完整正文（不含标题行，不截断），找不到返回空串。
func releaseSection(changelog, version string) string {
	var out []string
	in := false
	changelog = strings.TrimPrefix(changelog, "\ufeff") // 某些编辑器 / 脚本会写入 BOM，否则第一行的标题认不出来
	for _, line := range strings.Split(strings.ReplaceAll(changelog, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## [") {
			if in {
				break
			}
			in = strings.HasPrefix(line, "## ["+version+"]")
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// truncateUTF8 把 s 截到不超过 max 字节，且不会截断在多字节字符中间。
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	// 截断点若落在多字节字符中间，退回到上一个完整字符的末尾（最多退 3 个字节）；落在字符边界上则一个字都不丢。
	for i := 0; i < utf8.UTFMax && len(s) > 0 && !utf8.ValidString(s); i++ {
		s = s[:len(s)-1]
	}
	return s
}
