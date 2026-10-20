package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	good := map[string]Version{
		"v1.2.3": {1, 2, 3}, "1.2.3": {1, 2, 3}, " v0.0.1 ": {0, 0, 1}, "v10.20.30": {10, 20, 30},
	}
	for in, want := range good {
		got, ok := ParseVersion(in)
		if !ok || got != want {
			t.Errorf("ParseVersion(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
	// 开发版、带预发布后缀、格式不对的一律不是「正式版」，因此不会被自动更新覆盖。
	for _, bad := range []string{
		"", "dev", "v1.2", "v1.2.3.4", "v1.2.x", "v1.1.0-3-gabc1234", "v1.1.0-dirty", "abc1234", "v01.2.3", "v1..3", "v-1.2.3", "v1.2.3+meta",
	} {
		if v, ok := ParseVersion(bad); ok {
			t.Errorf("ParseVersion(%q) 应失败，却得到 %v", bad, v)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0}, {"1.0.1", "1.0.0", 1}, {"1.0.0", "1.0.1", -1},
		{"1.10.0", "1.9.0", 1}, {"2.0.0", "1.99.99", 1}, {"1.2.3", "1.3.0", -1},
	}
	for _, c := range cases {
		a, _ := ParseVersion(c.a)
		b, _ := ParseVersion(c.b)
		if got := a.Compare(b); got != c.want {
			t.Errorf("%s vs %s = %d want %d", c.a, c.b, got, c.want)
		}
	}
	v, _ := ParseVersion("1.2.3")
	if v.Tag() != "v1.2.3" || v.String() != "1.2.3" {
		t.Errorf("格式化: %s %s", v.Tag(), v.String())
	}
}

const goodManifest = `{
  "schema": 1, "version": "1.2.0", "tag": "v1.2.0", "published_at": "2026-10-20T10:00:00Z", "notes": "n",
  "assets": {
    "windows-amd64": {"name": "poolsvc-windows-amd64.zip", "size": 1234, "sha256": "` + "aa" + `00000000000000000000000000000000000000000000000000000000000000"}
  }
}`

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(goodManifest))
	if err != nil || m.Version != "1.2.0" || m.Assets["windows-amd64"].Size != 1234 {
		t.Fatalf("合法清单应通过: %+v %v", m, err)
	}

	mutate := func(f func(map[string]any)) []byte {
		var obj map[string]any
		json.Unmarshal([]byte(goodManifest), &obj)
		f(obj)
		b, _ := json.Marshal(obj)
		return b
	}
	asset := func(o map[string]any) map[string]any {
		return o["assets"].(map[string]any)["windows-amd64"].(map[string]any)
	}
	bad := map[string][]byte{
		"空":          nil,
		"不是 JSON":    []byte("<html>"),
		"版本号带后缀":     mutate(func(o map[string]any) { o["version"] = "1.2.0-rc1" }),
		"tag 不一致":    mutate(func(o map[string]any) { o["tag"] = "v1.2.1" }),
		"文件名含路径":     mutate(func(o map[string]any) { asset(o)["name"] = "../evil.zip" }),
		"文件名含斜杠":     mutate(func(o map[string]any) { asset(o)["name"] = "a/b.zip" }),
		"文件名含空格":     mutate(func(o map[string]any) { asset(o)["name"] = "a b.zip" }),
		"大小为 0":      mutate(func(o map[string]any) { asset(o)["size"] = 0 }),
		"大小过大":       mutate(func(o map[string]any) { asset(o)["size"] = MaxAssetBytes + 1 }),
		"sha256 太短":  mutate(func(o map[string]any) { asset(o)["sha256"] = "abc" }),
		"sha256 大写":  mutate(func(o map[string]any) { asset(o)["sha256"] = strings.Repeat("A", 64) }),
		"schema 为 0": mutate(func(o map[string]any) { o["schema"] = 0 }),
	}
	for name, raw := range bad {
		if _, err := ParseManifest(raw); err == nil {
			t.Errorf("%s：应被拒绝", name)
		}
	}
	// 更高版本的清单格式要给出明确的升级提示，而不是笼统的解析失败。
	if _, err := ParseManifest(mutate(func(o map[string]any) { o["schema"] = 2 })); err != ErrUnsupportedSchema {
		t.Errorf("更高 schema 应返回 ErrUnsupportedSchema: %v", err)
	}
}

func TestSignatureVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	raw := []byte(goodManifest)
	sig := Sign(priv, raw)

	if !VerifySignature(raw, sig, []ed25519.PublicKey{pub}) {
		t.Fatal("正确签名应通过")
	}
	if !VerifySignature(raw, sig, []ed25519.PublicKey{pub2, pub}) {
		t.Fatal("多把公钥任意一把匹配即可（密钥轮换）")
	}
	tampered := append([]byte(nil), raw...)
	tampered[10] ^= 1
	if VerifySignature(tampered, sig, []ed25519.PublicKey{pub}) {
		t.Fatal("内容被改动后签名必须失效")
	}
	if VerifySignature(raw, sig, []ed25519.PublicKey{pub2}) {
		t.Fatal("非受信公钥签的名不应通过")
	}
	for _, s := range []string{"", "not-base64!!", "QUJD", strings.Repeat("A", 5000)} {
		if VerifySignature(raw, []byte(s), []ed25519.PublicKey{pub}) {
			t.Errorf("畸形签名 %q 不应通过", s)
		}
	}
	if VerifySignature(raw, sig, nil) {
		t.Fatal("没有任何受信公钥时不应通过")
	}
}

func TestEmbeddedKeyIsValid(t *testing.T) {
	keys, err := ParseKeys(trustedKeys)
	if err != nil || len(keys) == 0 {
		t.Fatalf("内置公钥必须有效: %v", err)
	}
}

func TestReleaseNotes(t *testing.T) {
	cl := "# 变更记录\n\n## [1.2.0] - 2026-10-20\n\n### 新增\n- A\n- B\n\n## [1.1.0] - 2026-10-19\n\n### 修复\n- C\n"
	got := ReleaseNotes(cl, "1.2.0")
	if !strings.Contains(got, "- A") || !strings.Contains(got, "- B") || strings.Contains(got, "- C") || strings.Contains(got, "## [") {
		t.Errorf("只应取 1.2.0 小节正文: %q", got)
	}
	if ReleaseNotes(cl, "9.9.9") != "" {
		t.Error("找不到版本时应返回空串")
	}
	// CRLF 换行
	if !strings.Contains(ReleaseNotes(strings.ReplaceAll(cl, "\n", "\r\n"), "1.1.0"), "- C") {
		t.Error("应兼容 CRLF")
	}
	// 文件开头带 UTF-8 BOM，且版本标题就是第一行。
	if got := ReleaseNotes("\ufeff## [1.2.0] - x\n- first line heading\n", "1.2.0"); got != "- first line heading" {
		t.Errorf("应兼容 BOM: %q", got)
	}
	// 超长内容按字节截断，且不得截在汉字中间。
	long := "## [1.0.0]\n" + strings.Repeat("汉", maxNotesBytes)
	n := ReleaseNotes(long, "1.0.0")
	if len(n) > maxNotesBytes || !strings.HasSuffix(n, "汉") {
		t.Errorf("截断后应合法且不超限: len=%d", len(n))
	}
}

func TestBuildManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/poolsvc-windows-amd64.zip", "zipdata")
	writeFile(t, dir+"/poolsvc-linux-amd64.tar.gz", "tgzdata!")
	writeFile(t, dir+"/poolsvc-windows-amd64.zip.sha256", "ignored")
	writeFile(t, dir+"/unrelated.txt", "x")

	raw, err := BuildManifest(dir, "v1.2.0", "## [1.2.0] - x\n- hello\n", time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("生成的清单必须能被客户端解析: %v", err)
	}
	if len(m.Assets) != 2 || m.Assets["windows-amd64"].Size != 7 || m.Assets["linux-amd64"].Size != 8 {
		t.Fatalf("assets: %+v", m.Assets)
	}
	if m.Notes != "- hello" || m.PublishedAt != "2026-10-20T01:02:03Z" {
		t.Errorf("notes/published: %q %q", m.Notes, m.PublishedAt)
	}
	for _, bad := range []string{"1.2.0", "v1.2", "v1.2.0-rc1", ""} {
		if _, err := BuildManifest(dir, bad, "", time.Now()); err == nil {
			t.Errorf("标签 %q 应被拒绝", bad)
		}
	}
	if _, err := BuildManifest(t.TempDir(), "v1.2.0", "", time.Now()); err == nil {
		t.Error("目录里没有安装包时应报错")
	}
}
