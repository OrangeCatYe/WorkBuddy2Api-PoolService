package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
)

// ---- 本地模拟的发布服务器 ----

type release struct {
	srv  *httptest.Server
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey

	version  string // 如 "1.0.1"
	manifest []byte
	archive  []byte
	assetNm  string

	sigMode string // good（默认）/ none / tampered / wrongkey
	noMan   bool   // 清单 404
	corrupt bool   // 安装包内容被替换（长度不变）
	hits    atomic.Int32
}

func archiveName() string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return "poolsvc-" + runtime.GOOS + "-" + runtime.GOARCH + "." + ext
}

// newRelease 构造一个版本为 version 的发布。files 为包内文件（相对包根目录）。
func newRelease(t *testing.T, version string, files map[string]string) *release {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	r := &release{pub: pub, priv: priv, version: version, sigMode: "good", assetNm: archiveName()}

	top := strings.TrimSuffix(strings.TrimSuffix(r.assetNm, ".zip"), ".tar.gz") + "/"
	if runtime.GOOS == "windows" {
		in := map[string]string{}
		for k, v := range files {
			in[top+k] = v
		}
		r.archive = zipBytes(t, in)
	} else {
		var es []tarEntry
		for k, v := range files {
			es = append(es, tarEntry{name: top + k, body: v})
		}
		r.archive = tgzBytes(t, es)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, r.assetNm), string(r.archive))
	man, err := BuildManifest(dir, "v"+version, "## ["+version+"] - 2026-10-20\n- 测试更新说明\n", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.manifest = man

	mux := http.NewServeMux()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, _ *http.Request) {
		r.hits.Add(1)
		if r.noMan {
			http.NotFound(w, nil)
			return
		}
		w.Write(r.manifest)
	})
	mux.HandleFunc("/latest.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		switch r.sigMode {
		case "none":
			http.NotFound(w, nil)
		case "tampered":
			w.Write(Sign(r.priv, append([]byte("x"), r.manifest...)))
		case "wrongkey":
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			w.Write(Sign(other, r.manifest))
		default:
			w.Write(Sign(r.priv, r.manifest))
		}
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, "/"+r.assetNm) {
			http.NotFound(w, req)
			return
		}
		body := r.archive
		if r.corrupt {
			body = append([]byte(nil), r.archive...)
			body[len(body)/2] ^= 0xFF
		}
		w.Write(body)
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

type env struct {
	svc          *Service
	cfg          *config.Config
	rel          *release
	inst         string
	exe          string
	shutdown     atomic.Int32
	verifyCalled atomic.Int32
}

// newEnv 以 version 为当前版本，连接到 rel 提供的下载源。
func newEnv(t *testing.T, rel *release, version string) *env {
	t.Helper()
	e := &env{rel: rel, inst: filepath.Join(t.TempDir(), "inst")}
	e.exe = filepath.Join(e.inst, ExeName("poolsvc"))
	if err := os.MkdirAll(e.inst, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.AdminKey = "k"
	cfg.Update.Source = "test"
	cfg.Update.Sources = []config.UpdateSource{{
		ID: "test", Name: "本地测试源", ManifestURL: rel.srv.URL + "/latest.json", AssetURL: rel.srv.URL + "/dl/{tag}/{name}",
	}}
	e.cfg = cfg
	e.svc = New(func() *config.Config { return e.cfg }, version)
	e.svc.keys = []ed25519.PublicKey{rel.pub}
	e.svc.exePath = func() (string, error) { return e.exe, nil }
	e.svc.verifyExe = func(string, string) error { e.verifyCalled.Add(1); return nil }
	e.svc.SetShutdown(func() { e.shutdown.Add(1) })
	e.svc.refreshWritable()
	return e
}

func (e *env) check(t *testing.T) Status {
	t.Helper()
	if err := e.svc.Check(context.Background(), ""); err != nil {
		t.Fatalf("Check: %v", err)
	}
	return e.svc.Status()
}

func (e *env) waitState(t *testing.T, want string, d time.Duration) Status {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if st := e.svc.Status(); st.State == want {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := e.svc.Status()
	t.Fatalf("%s 内状态没有变成 %s：%s %q", d, want, st.State, st.Error)
	return st
}

func fakeFiles() map[string]string {
	return map[string]string{ExeName("poolsvc"): "NEW-EXE", "README.md": "new readme"}
}

// ---- 检查 ----

func TestCheckFindsNewerVersion(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	st := e.check(t)
	if st.State != StateAvailable || st.Latest == nil || st.Latest.Tag != "v1.0.1" {
		t.Fatalf("应发现新版本: %+v", st)
	}
	if !st.Latest.Signed || !st.Latest.Installable || st.Latest.Size == 0 || !strings.Contains(st.Latest.Notes, "测试更新说明") {
		t.Errorf("latest: %+v", st.Latest)
	}
	if !st.Current.CanApply || st.Current.Version != "v1.0.0" || st.Source != "test" || st.CheckedAt == nil {
		t.Errorf("current: %+v source=%s", st.Current, st.Source)
	}
}

func TestCheckNeverOffersDowngradeOrSameVersion(t *testing.T) {
	for _, cur := range []string{"v1.0.1", "v1.0.2", "v2.0.0"} {
		e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), cur)
		if st := e.check(t); st.State != StateUpToDate {
			t.Errorf("当前 %s、清单 1.0.1：应为已最新而不是 %s", cur, st.State)
		}
	}
}

func TestUnsignedManifestIsShownButNotInstallable(t *testing.T) {
	rel := newRelease(t, "1.0.1", fakeFiles())
	rel.sigMode = "none"
	e := newEnv(t, rel, "v1.0.0")
	st := e.check(t)
	if st.State != StateAvailable || st.Latest.Signed || st.Latest.Installable || !strings.Contains(st.Latest.Reason, "签名") {
		t.Fatalf("无签名：可展示但不可安装: %+v", st.Latest)
	}
	err := e.svc.Download()
	var rj *RejectError
	if !errors.As(err, &rj) {
		t.Fatalf("无签名的版本不允许下载安装: %v", err)
	}
}

func TestInvalidSignatureIsRejectedOutright(t *testing.T) {
	for _, mode := range []string{"tampered", "wrongkey"} {
		rel := newRelease(t, "1.0.1", fakeFiles())
		rel.sigMode = mode
		e := newEnv(t, rel, "v1.0.0")
		st := e.check(t)
		if st.State != StateError || !strings.Contains(st.Error, "签名") {
			t.Errorf("%s：有签名但验不过，必须报错而不是当作无签名: %+v", mode, st)
		}
		if st.Latest != nil {
			t.Errorf("%s：验签失败的清单内容不得出现在状态里", mode)
		}
		if err := e.svc.Download(); err == nil {
			t.Errorf("%s：不得下载", mode)
		}
	}
}

func TestCheckNetworkFailures(t *testing.T) {
	rel := newRelease(t, "1.0.1", fakeFiles())
	rel.noMan = true
	e := newEnv(t, rel, "v1.0.0")
	if st := e.check(t); st.State != StateError || !strings.Contains(st.Error, "latest.json") {
		t.Errorf("清单 404: %+v", st)
	}
	rel.srv.Close()
	if st := e.check(t); st.State != StateError || !strings.Contains(st.Error, "无法连接") {
		t.Errorf("服务器不可达: %+v", st)
	}
	// 失败后可以再次检查，不会卡在错误状态。
	e2 := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	e2.check(t)
	if st := e2.check(t); st.State != StateAvailable {
		t.Errorf("重复检查应保持可用: %s", st.State)
	}
}

func TestCheckUnknownSourceAndBusy(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	var rj *RejectError
	if err := e.svc.Check(context.Background(), "nope"); !errors.As(err, &rj) {
		t.Errorf("未知下载源应被拒绝: %v", err)
	}
	for _, busy := range []string{StateChecking, StateDownloading, StateApplying} {
		e.svc.mu.Lock()
		e.svc.state = busy
		e.svc.mu.Unlock()
		if err := e.svc.Check(context.Background(), ""); !errors.Is(err, ErrBusy) {
			t.Errorf("%s 时再检查应返回 ErrBusy: %v", busy, err)
		}
	}
}

func TestDevBuildCannotSelfUpdate(t *testing.T) {
	for _, v := range []string{"dev", "v1.0.0-3-gabc1234", "v1.0.0-dirty", "abc1234"} {
		e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), v)
		st := e.check(t)
		if st.Current.Release || st.Current.CanApply || !strings.Contains(st.Current.Reason, "开发版") {
			t.Errorf("%s：开发版不得自动更新: %+v", v, st.Current)
		}
		var rj *RejectError
		if err := e.svc.Download(); !errors.As(err, &rj) {
			t.Errorf("%s：不得下载: %v", v, err)
		}
	}
}

func TestCannotApplyWithoutAdminKeyOrWritableDir(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	e.cfg.AdminKey = ""
	st := e.check(t)
	if st.Current.CanApply || !strings.Contains(st.Current.Reason, "admin_key") {
		t.Errorf("admin_key 为空不得一键更新: %+v", st.Current)
	}
	e.cfg.AdminKey = "k"
	e.svc.writable = false
	if st := e.svc.Status(); st.Current.CanApply || !strings.Contains(st.Current.Reason, "写权限") {
		t.Errorf("目录不可写不得一键更新: %+v", st.Current)
	}
}

func TestRecheckKeepsReadyPackage(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	e.check(t)
	if err := e.svc.Download(); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, StateReady, 20*time.Second)
	if st := e.check(t); st.State != StateReady {
		t.Errorf("同一版本重复检查不应让已下载的安装包作废: %s", st.State)
	}
}

// ---- 下载 ----

func TestDownloadExtractsAndBecomesReady(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	e.check(t)
	if err := e.svc.Download(); err != nil {
		t.Fatal(err)
	}
	st := e.waitState(t, StateReady, 20*time.Second)
	stage := filepath.Join(e.svc.dir(), "stage", "1.0.1")
	if readFile(t, filepath.Join(stage, ExeName("poolsvc"))) != "NEW-EXE" || readFile(t, filepath.Join(stage, "README.md")) != "new readme" {
		t.Error("暂存目录内容不对")
	}
	if exists(filepath.Join(e.svc.dir(), "dl", "1.0.1", e.rel.assetNm)) {
		t.Error("解压完成后应删除压缩包")
	}
	_ = st
	if err := e.svc.Download(); err != nil {
		t.Errorf("ready 状态允许重新下载: %v", err)
	}
	e.waitState(t, StateReady, 20*time.Second)
}

func TestDownloadRejectsTamperedPackage(t *testing.T) {
	rel := newRelease(t, "1.0.1", fakeFiles())
	rel.corrupt = true
	e := newEnv(t, rel, "v1.0.0")
	e.check(t)
	if err := e.svc.Download(); err != nil {
		t.Fatal(err)
	}
	st := e.waitState(t, StateError, 20*time.Second)
	if !strings.Contains(st.Error, "校验失败") {
		t.Errorf("安装包被篡改应报校验失败: %q", st.Error)
	}
	if exists(filepath.Join(e.svc.dir(), "stage")) {
		t.Error("校验失败后不得有暂存目录")
	}
	if err := e.svc.Apply(); err == nil {
		t.Error("没有校验通过的安装包不得安装")
	}
	if e.shutdown.Load() != 0 {
		t.Error("不得触发退出")
	}
}

func TestDownloadRequiresCheckFirst(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	var rj *RejectError
	if err := e.svc.Download(); !errors.As(err, &rj) {
		t.Errorf("未检查就下载应被拒绝: %v", err)
	}
	if err := e.svc.Apply(); !errors.As(err, &rj) {
		t.Errorf("未下载就安装应被拒绝: %v", err)
	}
}

// ---- 安装前预检 ----

func readyEnv(t *testing.T, files map[string]string, version string) *env {
	t.Helper()
	e := newEnv(t, newRelease(t, "1.0.1", files), version)
	e.check(t)
	if err := e.svc.Download(); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, StateReady, 30*time.Second)
	return e
}

func TestApplyPreflightFailureLeavesEverythingRunning(t *testing.T) {
	e := readyEnv(t, fakeFiles(), "v1.0.0")
	e.svc.verifyExe = func(string, string) error { return errors.New("拒绝访问") }
	var rj *RejectError
	err := e.svc.Apply()
	if !errors.As(err, &rj) || !strings.Contains(err.Error(), "无法运行") {
		t.Fatalf("新程序跑不起来应在停止旧服务之前被拒绝: %v", err)
	}
	if e.shutdown.Load() != 0 {
		t.Error("预检失败不得触发退出")
	}
	if st := e.svc.Status(); st.State != StateReady {
		t.Errorf("预检失败后应回到 ready 可重试: %s", st.State)
	}
	if exists(filepath.Join(e.svc.dir(), planFile)) {
		t.Error("不应留下计划文件")
	}
}

func TestApplyFailsCleanlyWhenUpdaterCannotStart(t *testing.T) {
	e := readyEnv(t, fakeFiles(), "v1.0.0") // 暂存里的 "poolsvc" 只是一段文本，不是可执行文件
	var rj *RejectError
	err := e.svc.Apply()
	if !errors.As(err, &rj) || !strings.Contains(err.Error(), "更新程序") {
		t.Fatalf("更新程序拉不起来应明确报错: %v", err)
	}
	if e.shutdown.Load() != 0 {
		t.Fatal("更新程序没起来时绝不能让旧服务退出")
	}
	if st := e.svc.Status(); st.State != StateReady {
		t.Errorf("应回到 ready: %s", st.State)
	}
	if exists(filepath.Join(e.svc.dir(), planFile)) {
		t.Error("失败后应清掉计划文件")
	}
}

func TestApplyRefusesWhenDirNotWritable(t *testing.T) {
	e := readyEnv(t, fakeFiles(), "v1.0.0")
	e.svc.exePath = func() (string, error) { return filepath.Join(e.inst, "no-such-dir", "poolsvc"), nil }
	var rj *RejectError
	if err := e.svc.Apply(); !errors.As(err, &rj) {
		t.Fatalf("目录不可写应被拒绝: %v", err)
	}
	if e.shutdown.Load() != 0 {
		t.Error("不得触发退出")
	}
}

// ---- 端到端：从「安装」到服务换成新版本 ----

// 这是整条链路的真实演练：Service.Apply 写计划 → 拉起暂存包里的更新程序 → 主服务退出 →
// 更新程序备份、替换、拉起新版本、探活 → 新版本报告新版本号，结果文件回显。
func TestFullUpdateThroughService(t *testing.T) {
	addr := freeAddr(t)
	// 先造一个真正的「新版本」程序放进发布包。
	tmp := filepath.Join(t.TempDir(), ExeName("poolsvc"))
	makeHelper(t, tmp, "v1.0.1", false)
	newBin, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	rel := newRelease(t, "1.0.1", map[string]string{ExeName("poolsvc"): string(newBin), "README.md": "新 README"})

	e := newEnv(t, rel, "v1.0.0")
	e.cfg.Listen = addr
	e.svc.verifyExe = runVersionCheck // 用真实的预检：运行新程序的 version 子命令
	e.svc.args = []string{"-helper-listen", addr}
	e.svc.console = func() bool { return false }
	makeHelper(t, e.exe, "v1.0.0", false)
	writeFile(t, filepath.Join(e.inst, "README.md"), "旧 README")
	pid, kill := startOld(t, e.exe, addr)
	e.svc.pid = func() int { return pid }
	e.svc.SetShutdown(func() { kill() }) // 模拟主程序收到退出请求后优雅退出
	t.Cleanup(func() { quitServer(addr); time.Sleep(800 * time.Millisecond) })

	e.check(t)
	if err := e.svc.Download(); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, StateReady, 60*time.Second)
	if err := e.svc.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st := e.svc.Status(); st.State != StateApplying {
		t.Errorf("交给更新程序后应为 applying: %s", st.State)
	}

	// 更新完成后新版本应在同一地址上报告新版本号，并留下结果。
	waitHealth(t, addr, "v1.0.1", 90*time.Second)
	var res *Result
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if res = e.svc.Status().LastResult; res != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if res == nil || !res.OK || res.From != "v1.0.0" || res.To != "v1.0.1" {
		t.Fatalf("应写入成功结果: %+v", res)
	}
	if versionOf(e.exe) != "v1.0.1" || readFile(t, filepath.Join(e.inst, "README.md")) != "新 README" {
		t.Error("主程序与 README 应已更新")
	}
	if exists(filepath.Join(e.svc.dir(), resultFile)) {
		t.Error("结果文件被读取后应删除，避免下次启动重复提示")
	}
	if exists(filepath.Join(e.svc.dir(), planFile)) || exists(filepath.Join(e.svc.dir(), aliveFile)) {
		t.Error("更新程序结束后应清理计划与报到标记")
	}
	if len(readFile(t, filepath.Join(e.svc.dir(), applyLog))) == 0 {
		t.Error("应留有更新日志便于排查")
	}
}

// ---- 结果回显、清理、自动检查 ----

func TestStatusLoadsResultOnce(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.1")
	if e.svc.Status().LastResult != nil {
		t.Fatal("没有结果文件时应为空")
	}
	if err := WriteResult(e.svc.dir(), Result{OK: true, From: "v1.0.0", To: "v1.0.1", Message: "ok", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if r := e.svc.Status().LastResult; r == nil || !r.OK || r.To != "v1.0.1" {
		t.Fatalf("应读到结果: %+v", r)
	}
	if exists(filepath.Join(e.svc.dir(), resultFile)) {
		t.Error("读取后应删除文件")
	}
	if e.svc.Status().LastResult == nil {
		t.Error("进程内应保留结果直到重启")
	}
}

func TestCleanupRemovesStaleButNotWhileBusy(t *testing.T) {
	e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
	d := e.svc.dir()
	seed := func(planAge time.Duration) {
		writeFile(t, filepath.Join(d, "dl", "1.0.1", "a.zip.part"), "x")
		writeFile(t, filepath.Join(d, "stage", "1.0.1", "poolsvc"), "x")
		writeFile(t, filepath.Join(d, planFile), "{}")
		writeFile(t, filepath.Join(d, "backup", "v1.0.0-1", "files", "x"), "keep")
		at := time.Now().Add(-planAge)
		os.Chtimes(filepath.Join(d, planFile), at, at)
	}
	seed(time.Hour)
	e.svc.cleanup()
	if exists(filepath.Join(d, "dl")) || exists(filepath.Join(d, "stage")) || exists(filepath.Join(d, planFile)) {
		t.Error("过期的下载、暂存、计划应被清理")
	}
	if !exists(filepath.Join(d, "backup", "v1.0.0-1", "files", "x")) {
		t.Error("备份不应被清理（留作手动回滚）")
	}

	seed(time.Minute) // 计划文件很新：更新程序可能还在跑
	e.svc.cleanup()
	if !exists(filepath.Join(d, "stage")) || !exists(filepath.Join(d, planFile)) {
		t.Error("计划文件很新时不得清理，更新程序可能还在使用暂存目录")
	}

	seed(time.Hour)
	e.svc.mu.Lock()
	e.svc.state = StateReady
	e.svc.mu.Unlock()
	e.svc.cleanup()
	if !exists(filepath.Join(d, "stage")) {
		t.Error("有已就绪的安装包时不得清理")
	}
}

func TestAutoCheckLoop(t *testing.T) {
	oldTick, oldFirst := autoTick, autoFirstDelay
	autoTick, autoFirstDelay = 20*time.Millisecond, 0
	t.Cleanup(func() { autoTick, autoFirstDelay = oldTick, oldFirst })

	run := func(auto bool) (*env, func()) {
		e := newEnv(t, newRelease(t, "1.0.1", fakeFiles()), "v1.0.0")
		e.cfg.Update.AutoCheck = auto
		e.cfg.Update.CheckInterval = config.Duration{Duration: time.Hour}
		ctx, cancel := contextWithCancel(t)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); e.svc.Run(ctx) }()
		return e, func() { cancel(); wg.Wait() }
	}

	on, stopOn := run(true)
	defer stopOn()
	deadline := time.Now().Add(5 * time.Second)
	for on.rel.hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // 间隔是 1 小时：之后不应再次检查
	if n := on.rel.hits.Load(); n != 1 {
		t.Errorf("开启自动检查：应只检查 1 次，实际 %d", n)
	}
	if st := on.svc.Status(); st.State != StateAvailable {
		t.Errorf("自动检查只发现更新，不下载不安装: %s", st.State)
	}

	off, stopOff := run(false)
	defer stopOff()
	time.Sleep(400 * time.Millisecond)
	if n := off.rel.hits.Load(); n != 0 {
		t.Errorf("默认关闭：不应有任何网络请求，实际 %d", n)
	}
}

func TestProbeAddr(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:7863": "127.0.0.1:7863", "0.0.0.0:7863": "127.0.0.1:7863", ":7863": "127.0.0.1:7863",
		"[::]:7863": "127.0.0.1:7863", "192.168.1.5:80": "192.168.1.5:80", "garbage": "127.0.0.1:7863",
	} {
		if got := probeAddr(in); got != want {
			t.Errorf("probeAddr(%q) = %q want %q", in, got, want)
		}
	}
}

func TestVersionCheckRunsRealBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), ExeName("poolsvc"))
	makeHelper(t, exe, "v3.1.4", false)
	if err := runVersionCheck(exe, "v3.1.4"); err != nil {
		t.Errorf("版本一致应通过: %v", err)
	}
	if err := runVersionCheck(exe, "v3.1.5"); err == nil {
		t.Error("版本不一致应失败")
	}
	garbage := filepath.Join(t.TempDir(), ExeName("poolsvc"))
	writeFile(t, garbage, "not an executable")
	if err := runVersionCheck(garbage, "v1.0.0"); err == nil {
		t.Error("不是可执行文件应失败")
	}
}
