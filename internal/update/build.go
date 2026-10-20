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
func ReleaseNotes(changelog, version string) string {
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
	notes := strings.TrimSpace(strings.Join(out, "\n"))
	if len(notes) > maxNotesBytes {
		notes = notes[:maxNotesBytes]
		// 不要截断在多字节字符中间。
		for len(notes) > 0 && notes[len(notes)-1]&0xC0 == 0x80 {
			notes = notes[:len(notes)-1]
		}
		if len(notes) > 0 && notes[len(notes)-1]&0x80 != 0 {
			notes = notes[:len(notes)-1]
		}
	}
	return notes
}
