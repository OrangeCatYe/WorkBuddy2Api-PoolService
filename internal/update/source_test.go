package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
)

func TestValidateURL(t *testing.T) {
	ok := []string{
		"https://github.com/a/b/releases/latest/download/latest.json",
		"https://mirror.example.com:8443/x",
		"http://127.0.0.1:8080/latest.json",
		"http://localhost/x",
		"http://[::1]:9/x",
	}
	for _, u := range ok {
		if err := ValidateURL(u); err != nil {
			t.Errorf("%s 应通过: %v", u, err)
		}
	}
	bad := []string{
		"", "http://example.com/x", "ftp://example.com/x", "file:///etc/passwd", "https://", "https://user:pw@example.com/x",
		"javascript:alert(1)", "//example.com/x", "http://192.168.1.5/x", "http://127.0.0.1.evil.com/x",
	}
	for _, u := range bad {
		if err := ValidateURL(u); err == nil {
			t.Errorf("%q 应被拒绝", u)
		}
	}
}

func goodSource() config.UpdateSource {
	return config.UpdateSource{ID: "gitee", Name: "Gitee 镜像", ManifestURL: "https://gitee.com/x/y/raw/latest.json", AssetURL: "https://gitee.com/x/y/releases/download/{tag}/{name}"}
}

func TestValidateSource(t *testing.T) {
	if err := ValidateSource(goodSource()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*config.UpdateSource){
		"ID 含大写":       func(s *config.UpdateSource) { s.ID = "Gitee" },
		"ID 含空格":       func(s *config.UpdateSource) { s.ID = "a b" },
		"ID 为空":        func(s *config.UpdateSource) { s.ID = "" },
		"ID 过长":        func(s *config.UpdateSource) { s.ID = strings.Repeat("a", 33) },
		"占用内置 ID":      func(s *config.UpdateSource) { s.ID = "github" },
		"名称为空":         func(s *config.UpdateSource) { s.Name = "  " },
		"清单不是 https":   func(s *config.UpdateSource) { s.ManifestURL = "http://example.com/latest.json" },
		"安装包模板缺少 name": func(s *config.UpdateSource) { s.AssetURL = "https://example.com/pkg.zip" },
		"安装包不是 https":  func(s *config.UpdateSource) { s.AssetURL = "http://example.com/{name}" },
		"安装包带账号密码":     func(s *config.UpdateSource) { s.AssetURL = "https://u:p@example.com/{name}" },
	}
	for name, mut := range cases {
		s := goodSource()
		mut(&s)
		if err := ValidateSource(s); err == nil {
			t.Errorf("%s：应被拒绝", name)
		}
	}
	dup := []config.UpdateSource{goodSource(), goodSource()}
	if ValidateSources(dup) == nil {
		t.Error("重复 ID 应被拒绝")
	}
	var many []config.UpdateSource
	for i := 0; i < 9; i++ {
		s := goodSource()
		s.ID = "s" + string(rune('a'+i))
		many = append(many, s)
	}
	if ValidateSources(many) == nil {
		t.Error("超过数量上限应被拒绝")
	}
}

func TestSourcesAndFind(t *testing.T) {
	bad := goodSource()
	bad.ID = "BAD"
	cfg := config.UpdateConfig{Source: "gitee", Sources: []config.UpdateSource{goodSource(), bad}}
	list := Sources(cfg)
	if len(list) != 2 || list[0].ID != "github" || !list[0].Builtin || list[1].ID != "gitee" {
		t.Fatalf("内置源在前，非法自定义源被跳过: %+v", list)
	}
	if s, err := Find(cfg, ""); err != nil || s.ID != "gitee" {
		t.Errorf("省略 ID 时应取配置选中的源: %+v %v", s, err)
	}
	if s, err := Find(cfg, "github"); err != nil || s.ID != "github" {
		t.Errorf("按 ID 取内置源: %+v %v", s, err)
	}
	if _, err := Find(cfg, "nope"); err == nil {
		t.Error("未知源应报错")
	}
	if _, err := Find(cfg, "BAD"); err == nil {
		t.Error("被跳过的非法源不可选")
	}
}

func TestSourceURLs(t *testing.T) {
	gh := Builtin()
	if gh.ManifestURL != "https://github.com/OrangeCatYe/WorkBuddy2Api-PoolService/releases/latest/download/latest.json" {
		t.Errorf("官方清单地址: %s", gh.ManifestURL)
	}
	if got := gh.AssetFor("v1.2.0", "poolsvc-windows-amd64.zip"); got != "https://github.com/OrangeCatYe/WorkBuddy2Api-PoolService/releases/download/v1.2.0/poolsvc-windows-amd64.zip" {
		t.Errorf("官方安装包地址: %s", got)
	}
	if gh.SigURL() != gh.ManifestURL+".sig" {
		t.Errorf("签名地址: %s", gh.SigURL())
	}
	s := Source{ManifestURL: "https://m.example.com/raw/latest.json?token=1", AssetURL: "https://m.example.com/{version}/{name}"}
	if s.SigURL() != "https://m.example.com/raw/latest.json.sig?token=1" {
		t.Errorf("带查询参数的签名地址: %s", s.SigURL())
	}
	if got := s.AssetFor("v1.2.0", "a.zip"); got != "https://m.example.com/1.2.0/a.zip" {
		t.Errorf("{version} 展开: %s", got)
	}
}

func TestProxyParseAndMask(t *testing.T) {
	for _, p := range []string{"http://127.0.0.1:7890", "https://p.example.com:443", "socks5://127.0.0.1:1080", "socks5h://u:p@h:1"} {
		if _, err := ParseProxy(p); err != nil {
			t.Errorf("%s 应通过: %v", p, err)
		}
	}
	for _, p := range []string{"", "127.0.0.1:7890", "ftp://h:1", "http://", "javascript:1"} {
		if _, err := ParseProxy(p); err == nil {
			t.Errorf("%q 应被拒绝", p)
		}
	}
	if got := MaskProxy("http://alice:s3cret@h:8080"); strings.Contains(got, "s3cret") || got != "http://alice:***@h:8080" {
		t.Errorf("密码必须被隐藏且保持可读: %s", got)
	}
	if got := MaskProxy("http://h:8080"); got != "http://h:8080" {
		t.Errorf("无密码时原样返回: %s", got)
	}
	// 错误信息里也不能泄漏密码。
	if _, err := ParseProxy("ftp://alice:s3cret@h:1"); err != nil && strings.Contains(err.Error(), "s3cret") {
		t.Errorf("错误信息泄漏了密码: %v", err)
	}
	if _, err := NewHTTPClient("not a proxy"); err == nil {
		t.Error("非法代理应在创建客户端时报错")
	}
}

// ---- 下载 ----

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func newClient(t *testing.T) *http.Client {
	t.Helper()
	c, err := NewHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDownloadOK(t *testing.T) {
	body := []byte(strings.Repeat("poolsvc", 5000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "dl", "a.zip")
	var last int64
	err := Download(context.Background(), newClient(t), srv.URL, dst, int64(len(body)), sum(body), func(d int64) { last = d })
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, dst) != string(body) || last != int64(len(body)) {
		t.Error("内容或进度不对")
	}
	if exists(dst + ".part") {
		t.Error("成功后不应留下 .part")
	}
}

func TestDownloadRejectsBadContent(t *testing.T) {
	body := []byte(strings.Repeat("x", 1000))
	good := sum(body)
	cases := map[string]struct {
		serve func(w http.ResponseWriter)
		size  int64
		sha   string
	}{
		"哈希不一致":      {func(w http.ResponseWriter) { w.Write(body) }, 1000, sum([]byte("other"))},
		"内容被替换（同长度）": {func(w http.ResponseWriter) { w.Write([]byte(strings.Repeat("y", 1000))) }, 1000, good},
		"比声明的短":      {func(w http.ResponseWriter) { w.Write(body[:500]) }, 1000, good},
		"比声明的长":      {func(w http.ResponseWriter) { w.Write(append(body, 'z')) }, 1000, good},
		"HTTP 404":   {func(w http.ResponseWriter) { w.WriteHeader(404) }, 1000, good},
		"HTTP 500":   {func(w http.ResponseWriter) { w.WriteHeader(500) }, 1000, good},
	}
	for name, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.serve(w) }))
		dst := filepath.Join(t.TempDir(), "a.zip")
		err := Download(context.Background(), newClient(t), srv.URL, dst, c.size, c.sha, nil)
		srv.Close()
		if err == nil {
			t.Errorf("%s：应失败", name)
		}
		if exists(dst) || exists(dst+".part") {
			t.Errorf("%s：失败后不得留下任何文件（否则可能被当成完整安装包）", name)
		}
	}
	if err := Download(context.Background(), newClient(t), "http://127.0.0.1:1", filepath.Join(t.TempDir(), "a"), 0, good, nil); err == nil {
		t.Error("大小为 0 应被拒绝")
	}
}

func TestDownloadIdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-release // 卡住不再发数据
	}))
	defer srv.Close()
	defer close(release)
	dst := filepath.Join(t.TempDir(), "a.zip")
	start := time.Now()
	err := download(context.Background(), newClient(t), srv.URL, dst, 1000, sum([]byte("x")), 300*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "没有收到数据") {
		t.Fatalf("长时间无数据应中断: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("空闲超时没有及时生效: %s", time.Since(start))
	}
	if exists(dst + ".part") {
		t.Error("中断后不应留下 .part")
	}
}

func TestDownloadRespectsContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	err := Download(ctx, newClient(t), srv.URL, filepath.Join(t.TempDir(), "a"), 100, sum([]byte("x")), nil)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("取消应立即生效: err=%v after %s", err, time.Since(start))
	}
}

func TestRedirectToPlainHTTPIsRejected(t *testing.T) {
	// 回环地址允许 http，所以用一个「看起来是 http 外网」的 Location 来验证重定向校验。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/evil", http.StatusFound)
	}))
	defer srv.Close()
	_, _, err := fetchSmall(context.Background(), newClient(t), srv.URL, 1000)
	if err == nil {
		t.Fatal("被重定向到明文 http 外网必须失败")
	}
	_ = os.Stdout
}

func TestFetchSmallLimitsAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("hello"))
		case "/big":
			w.Write([]byte(strings.Repeat("a", 5000)))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := newClient(t)
	if b, code, err := fetchSmall(context.Background(), c, srv.URL+"/ok", 100); err != nil || code != 200 || string(b) != "hello" {
		t.Errorf("ok: %q %d %v", b, code, err)
	}
	if b, code, err := fetchSmall(context.Background(), c, srv.URL+"/nope", 100); err != nil || code != 404 || b != nil {
		t.Errorf("404 应只返回状态码: %q %d %v", b, code, err)
	}
	if _, _, err := fetchSmall(context.Background(), c, srv.URL+"/big", 100); err == nil {
		t.Error("超过上限必须报错")
	}
}
