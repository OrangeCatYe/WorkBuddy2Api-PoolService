package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/sysproc"
)

// 服务状态。
const (
	StateIdle        = "idle"        // 还没检查过
	StateChecking    = "checking"    // 正在拉取清单
	StateUpToDate    = "up_to_date"  // 已是最新
	StateAvailable   = "available"   // 有新版本
	StateDownloading = "downloading" // 正在下载与解压
	StateReady       = "ready"       // 已下载并校验，可以安装
	StateApplying    = "applying"    // 已交给更新程序，服务即将重启
	StateError       = "error"
)

var (
	// ErrBusy 当前状态不允许该操作（正在检查 / 下载 / 安装）。
	ErrBusy = errors.New("正在进行其他更新操作，请稍后")
)

// 时间参数，测试里会缩短。
var (
	aliveWait      = 10 * time.Second // 等更新程序报到的最长时间
	autoTick       = time.Minute      // 自动检查的轮询粒度
	autoFirstDelay = 30 * time.Second // 启动后多久做第一次自动检查
)

// RejectError 请求本身不被允许（前置条件不满足），带面向用户的原因。
type RejectError struct{ Msg string }

func (e *RejectError) Error() string { return e.Msg }

func reject(format string, a ...any) error { return &RejectError{Msg: fmt.Sprintf(format, a...)} }

// Progress 下载进度。
type Progress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// Current 当前运行的版本及能否自动更新。
type Current struct {
	Version  string `json:"version"`
	Release  bool   `json:"release"`   // 是否为正式版（vX.Y.Z）
	CanApply bool   `json:"can_apply"` // 能否一键安装
	Reason   string `json:"reason,omitempty"`
	Mode     string `json:"mode"` // self：更新程序接力重启；supervised：由服务管理器重启
	Platform string `json:"platform"`
}

// Latest 清单里的最新版本。
type Latest struct {
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Signed      bool   `json:"signed"`      // 清单是否带有效签名
	Installable bool   `json:"installable"` // 已签名且有本平台安装包
	Reason      string `json:"reason,omitempty"`
}

// Status 面板读取的完整状态。
type Status struct {
	State      string     `json:"state"`
	Current    Current    `json:"current"`
	Latest     *Latest    `json:"latest,omitempty"`
	Source     string     `json:"source"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	Progress   *Progress  `json:"progress,omitempty"`
	LastResult *Result    `json:"last_result,omitempty"`
}

// Service 软件更新服务。
type Service struct {
	cfg     func() *config.Config
	version string
	keys    []ed25519.PublicKey

	// 以下字段用于测试替换。
	newClient  func(proxy string) (*http.Client, error)
	exePath    func() (string, error)
	pid        func() int
	args       []string
	supervised bool
	verifyExe  func(exe, want string) error // 运行 `exe version` 确认新程序能跑且版本对
	console    func() bool

	mu       sync.Mutex
	state    string
	errMsg   string
	source   string
	checked  time.Time
	manifest *Manifest
	signed   bool
	stage    string
	last     *Result
	writable bool
	progress atomic.Pointer[Progress]
	done     atomic.Int64
	rootCtx  context.Context
	shutdown func()
}

// New 创建服务。version 为当前程序版本（构建时注入）。
func New(cfg func() *config.Config, version string) *Service {
	keys, err := ParseKeys(trustedKeys)
	if err != nil {
		log.Printf("WARN: [update] 内置公钥无效，将无法安装更新: %v", err)
	}
	s := &Service{
		cfg: cfg, version: version, keys: keys,
		newClient:  NewHTTPClient,
		exePath:    executablePath,
		pid:        os.Getpid,
		args:       append([]string(nil), os.Args[1:]...),
		supervised: sysproc.UnderSupervisor(),
		verifyExe:  runVersionCheck,
		console:    sysproc.StdoutIsConsole,
		state:      StateIdle,
		rootCtx:    context.Background(),
	}
	s.refreshWritable()
	return s
}

// SetShutdown 注入「请求主程序优雅退出」的回调，安装更新时使用。
func (s *Service) SetShutdown(fn func()) { s.shutdown = fn }

func executablePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Abs(p)
}

// probeWritable 探测安装目录是否可写（没有写权限时无法自动替换文件）。只探测，不碰共享状态。
func (s *Service) probeWritable() bool {
	exe, err := s.exePath()
	if err != nil {
		return false
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".poolsvc-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// refreshWritable 重新探测并记录结果。仅限构造时（尚未并发）或已持 s.mu 时调用；
// 其他场景用 probeWritable 探测后在锁内写入 s.writable。
func (s *Service) refreshWritable() { s.writable = s.probeWritable() }

func (s *Service) dir() string {
	d := s.cfg().DataDir
	if abs, err := filepath.Abs(d); err == nil {
		d = abs
	}
	return filepath.Join(d, "update")
}

func (s *Service) current() Current {
	v, release := ParseVersion(s.version)
	c := Current{Version: s.version, Release: release, Platform: PlatformKey(), Mode: "self"}
	if s.supervised {
		c.Mode = "supervised"
	}
	switch {
	case !release:
		c.Reason = fmt.Sprintf("当前是开发版 / 源码构建（%s），不支持自动更新", s.version)
	case !s.writable:
		c.Reason = "程序所在目录没有写权限，无法自动替换文件，请手动下载安装"
	case s.cfg().AdminKey == "":
		c.Reason = "需要先在 config.json 中设置 admin_key 才能使用一键更新"
	default:
		c.CanApply = true
	}
	if release {
		c.Version = v.Tag()
	}
	return c
}

// Status 当前状态快照。
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		if r := readResult(s.dir()); r != nil {
			s.last = r
			_ = os.Remove(filepath.Join(s.dir(), resultFile))
		}
	}
	st := Status{State: s.state, Current: s.current(), Source: s.source, Error: s.errMsg, LastResult: s.last}
	if !s.checked.IsZero() {
		t := s.checked
		st.CheckedAt = &t
	}
	if s.manifest != nil {
		st.Latest = s.latestLocked()
	}
	if s.state == StateDownloading {
		if p := s.progress.Load(); p != nil {
			st.Progress = &Progress{Done: s.done.Load(), Total: p.Total}
		}
	}
	return st
}

func (s *Service) latestLocked() *Latest {
	m := s.manifest
	l := &Latest{Version: m.Version, Tag: m.Tag, Notes: m.Notes, PublishedAt: m.PublishedAt, Signed: s.signed}
	asset, ok := m.Assets[PlatformKey()]
	switch {
	case !ok:
		l.Reason = "该版本没有适配当前平台（" + PlatformKey() + "）的安装包"
	case !s.signed:
		l.Size = asset.Size
		l.Reason = "该版本的更新清单没有有效签名，不允许自动安装，请到发布页手动下载"
	default:
		l.Size = asset.Size
		l.Installable = true
	}
	return l
}

// Check 检查更新。网络与校验类失败记入状态（State=error），不作为返回错误；
// 只有请求本身不合法（未知下载源）或正忙时才返回错误。
func (s *Service) Check(ctx context.Context, sourceID string) error {
	s.mu.Lock()
	switch s.state {
	case StateChecking, StateDownloading, StateApplying:
		s.mu.Unlock()
		return ErrBusy
	}
	cfg := s.cfg().Update
	src, err := Find(cfg, sourceID)
	if err != nil {
		s.mu.Unlock()
		return reject("%v", err)
	}
	prev := s.state
	s.state, s.errMsg, s.source = StateChecking, "", src.ID
	s.mu.Unlock()

	m, signed, err := s.fetchManifest(ctx, cfg, src)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = time.Now()
	if err != nil {
		s.state, s.errMsg = StateError, err.Error()
		return nil
	}
	s.refreshWritable()
	cur, curOK := ParseVersion(s.version)
	latest, _ := ParseVersion(m.Version)
	// 已经下载好同一个版本时保持 ready，不让用户的重复检查把已就绪的安装包作废。
	sameReady := prev == StateReady && s.manifest != nil && s.manifest.Version == m.Version && s.signed == signed
	s.manifest, s.signed = m, signed
	switch {
	case curOK && latest.Compare(cur) <= 0:
		s.state = StateUpToDate // 不是更高版本一律视为已最新，也就不会降级
	case sameReady:
		s.state = StateReady
	default:
		s.state = StateAvailable
	}
	return nil
}

func (s *Service) fetchManifest(ctx context.Context, cfg config.UpdateConfig, src Source) (*Manifest, bool, error) {
	client, err := s.newClient(cfg.Proxy)
	if err != nil {
		return nil, false, err
	}
	raw, code, err := fetchSmall(ctx, client, src.ManifestURL, maxManifestBytes)
	switch {
	case err != nil:
		return nil, false, fmt.Errorf("无法连接下载源「%s」：%w", src.Name, err)
	case code == http.StatusNotFound:
		// 同一标签重发时 Release 会有几秒空窗；老版本的 Release 也没有清单。
		return nil, false, fmt.Errorf("下载源「%s」上没有找到更新清单（latest.json），可能正在发布中或该源尚未同步", src.Name)
	case code >= 400:
		return nil, false, fmt.Errorf("下载源「%s」返回 HTTP %d", src.Name, code)
	}

	signed := false
	sig, scode, serr := fetchSmall(ctx, client, src.SigURL(), maxSigBytes)
	if serr == nil && scode == http.StatusOK {
		if !VerifySignature(raw, sig, s.keys) {
			// 有签名但验不过：内容被改过，或来自不受信任的发布者。这和「没有签名」不同，直接拒绝。
			return nil, false, errors.New("更新清单的签名校验失败：内容可能被篡改，或该下载源不是官方发布。已拒绝")
		}
		signed = true
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, false, err
	}
	return m, signed, nil
}

// Download 在后台下载并校验最新版本，进度通过 Status 轮询。
func (s *Service) Download() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case StateChecking, StateDownloading, StateApplying:
		return ErrBusy
	}
	if s.manifest == nil || (s.state != StateAvailable && s.state != StateReady && s.state != StateError) {
		return reject("请先检查更新")
	}
	cur := s.current()
	if !cur.CanApply {
		return reject("%s", cur.Reason)
	}
	l := s.latestLocked()
	if !l.Installable {
		return reject("%s", l.Reason)
	}
	cfg := s.cfg().Update
	src, err := Find(cfg, s.source)
	if err != nil {
		return reject("%v", err)
	}
	m, asset := s.manifest, s.manifest.Assets[PlatformKey()]
	s.state, s.errMsg = StateDownloading, ""
	s.done.Store(0)
	s.progress.Store(&Progress{Total: asset.Size})
	ctx := s.rootCtx
	go s.runDownload(ctx, cfg, src, m, asset)
	return nil
}

func (s *Service) runDownload(ctx context.Context, cfg config.UpdateConfig, src Source, m *Manifest, asset Asset) {
	fail := func(err error) {
		s.mu.Lock()
		s.state, s.errMsg = StateError, err.Error()
		s.mu.Unlock()
		log.Printf("[update] 下载失败: %v", err)
	}
	client, err := s.newClient(cfg.Proxy)
	if err != nil {
		fail(err)
		return
	}
	url := src.AssetFor(m.Tag, asset.Name)
	if err := ValidateURL(url); err != nil {
		fail(fmt.Errorf("安装包地址无效: %w", err))
		return
	}
	root := s.dir()
	archive := filepath.Join(root, "dl", m.Version, asset.Name)
	stage := filepath.Join(root, "stage", m.Version)
	log.Printf("[update] 开始下载 %s（%d 字节）", asset.Name, asset.Size)
	err = Download(ctx, client, url, archive, asset.Size, asset.SHA256, func(done int64) { s.done.Store(done) })
	if err != nil {
		fail(err)
		return
	}
	if _, err := Extract(archive, stage, runtime.GOOS); err != nil {
		fail(err)
		return
	}
	_ = os.Remove(archive)
	s.mu.Lock()
	s.state, s.stage = StateReady, stage
	s.mu.Unlock()
	log.Printf("[update] v%s 已下载并校验，等待安装", m.Version)
}

// Apply 启动更新程序并请求主程序退出。成功返回表示更新程序已经起来，服务随后会重启。
func (s *Service) Apply() error {
	s.mu.Lock()
	if s.state == StateApplying {
		s.mu.Unlock()
		return ErrBusy
	}
	if s.state != StateReady || s.manifest == nil {
		s.mu.Unlock()
		return reject("没有已下载的更新，请先下载")
	}
	cur := s.current()
	if !cur.CanApply {
		s.mu.Unlock()
		return reject("%s", cur.Reason)
	}
	m, stage, supervised := s.manifest, s.stage, s.supervised
	s.state = StateApplying
	s.mu.Unlock()

	err := s.apply(m, stage, supervised)

	s.mu.Lock()
	if err != nil {
		s.state, s.errMsg = StateReady, "" // 安装包仍然有效，可以重试
	}
	s.mu.Unlock()
	return err
}

func (s *Service) apply(m *Manifest, stage string, supervised bool) error {
	exe, err := s.exePath()
	if err != nil {
		return fmt.Errorf("无法确定程序路径: %w", err)
	}
	w := s.probeWritable()
	s.mu.Lock()
	s.writable = w
	s.mu.Unlock()
	if !w {
		return reject("程序所在目录没有写权限，无法自动替换文件")
	}
	mainRel := ExeName("poolsvc")
	newExe := filepath.Join(stage, mainRel)
	if _, err := os.Stat(newExe); err != nil {
		return reject("暂存目录里没有新版本主程序，请重新下载")
	}
	// 先让新程序自己报一次版本：架构不对、被安全软件拦截、文件损坏，都在停止旧服务之前暴露。
	if err := s.verifyExe(newExe, m.Tag); err != nil {
		return reject("新版本程序无法运行（%v）。可能被安全软件拦截，请检查后重试，或手动安装", err)
	}

	cfg := s.cfg()
	cwd, _ := os.Getwd()
	updDir := s.dir()
	plan := &Plan{
		Schema: 1, PID: s.pid(), ExePath: exe, MainRel: mainRel,
		Args: s.args, Cwd: cwd, ProbeAddr: probeAddr(cfg.Listen),
		FromVersion: s.version, ToVersion: m.Tag, StageDir: stage, UpdateDir: updDir,
		Visible: s.console(), GraceSeconds: int(defaultGrace / time.Second),
	}
	if cfgPath := cfg.Path(); cfgPath != "" {
		if abs, err := filepath.Abs(cfgPath); err == nil {
			plan.ExtraBackups = append(plan.ExtraBackups, abs)
		}
	}
	if dd, err := filepath.Abs(cfg.DataDir); err == nil {
		if states, _ := filepath.Glob(filepath.Join(dd, "*", "state.json")); len(states) > 0 {
			plan.ExtraBackups = append(plan.ExtraBackups, states...)
		}
	}
	for rel, mode := range PackageRules(runtime.GOOS) {
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(rel))); err == nil {
			plan.Files = append(plan.Files, PlanFile{Rel: rel, Mode: mode})
		}
	}

	if err := os.MkdirAll(updDir, 0o755); err != nil {
		return err
	}
	if supervised {
		// 被服务管理器托管：更新程序作为子进程会随服务的 cgroup 一起被清理，且自行拉起新进程
		// 会和服务管理器的重启抢端口。所以由主进程自己替换文件，再退出让服务管理器重启。
		res := ApplyInPlace(plan, func(f string, a ...any) { log.Printf("[update] "+f, a...) })
		_ = WriteResult(updDir, res)
		if !res.OK {
			return errors.New(res.Message)
		}
		s.requestShutdown()
		return nil
	}

	planPath := filepath.Join(updDir, planFile)
	alive := filepath.Join(updDir, aliveFile)
	_ = os.Remove(alive)
	if err := WritePlan(planPath, plan); err != nil {
		return err
	}
	proc, err := sysproc.Start(sysproc.StartOptions{Exe: newExe, Args: []string{"update-apply", "-plan", planPath}, Dir: filepath.Dir(exe)})
	if err != nil {
		_ = os.Remove(planPath)
		return reject("无法启动更新程序（%v）。可能被安全软件拦截，请检查后重试，或手动安装", err)
	}
	// 等更新程序报到。它没起来就不退出：旧服务照常运行，不会出现服务停了更新却没发生。
	deadline := time.Now().Add(aliveWait)
	for {
		if _, err := os.Stat(alive); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = proc.Kill()
			_ = os.Remove(planPath)
			return reject("更新程序启动后没有响应，已取消。可能被安全软件拦截，请检查后重试，或手动安装")
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("[update] 更新程序已就绪（pid %d），服务即将退出并重启为 %s", proc.Pid, m.Tag)
	s.requestShutdown()
	return nil
}

// requestShutdown 稍等片刻再退出，让触发更新的 HTTP 响应先送达浏览器。
func (s *Service) requestShutdown() {
	fn := s.shutdown
	if fn == nil {
		return
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		fn()
	}()
}

// probeAddr 把监听地址转成本机可访问的探活地址。
func probeAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "127.0.0.1:7863"
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// runVersionCheck 运行 `exe version` 确认新程序能执行且版本是预期的。
func runVersionCheck(exe, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "version").Output()
	if err != nil {
		return err
	}
	got := strings.TrimSpace(string(out))
	if !sameVersion(got, want) {
		return fmt.Errorf("新程序报告的版本是 %q，应为 %q", got, want)
	}
	return nil
}

// Run 后台循环：按配置自动检查更新；启动一段时间后清理上次更新留下的暂存文件。
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.rootCtx = ctx
	s.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Minute):
			s.cleanup()
		}
	}()

	var lastAuto time.Time
	start := time.Now()
	t := time.NewTicker(autoTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		u := s.cfg().Update
		if !u.AutoCheck {
			continue
		}
		due := lastAuto.IsZero() && time.Since(start) >= autoFirstDelay
		if !lastAuto.IsZero() && time.Since(lastAuto) >= u.CheckInterval.Duration {
			due = true
		}
		if !due {
			continue
		}
		lastAuto = time.Now()
		cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := s.Check(cctx, "")
		cancel()
		if err != nil && !errors.Is(err, ErrBusy) {
			log.Printf("[update] 自动检查: %v", err)
			continue
		}
		if st := s.Status(); st.State == StateError {
			log.Printf("[update] 自动检查失败: %s", st.Error) // 自动检查只记日志，不打扰用户
		} else if st.State == StateAvailable && st.Latest != nil {
			log.Printf("[update] 发现新版本 %s，可在「设置 → 软件更新」中安装", st.Latest.Tag)
		}
	}
}

// cleanup 清理上次更新留下的下载、暂存与计划文件。正在更新时（计划文件很新）不动。
func (s *Service) cleanup() {
	d := s.dir()
	if fi, err := os.Stat(filepath.Join(d, planFile)); err == nil && time.Since(fi.ModTime()) < 10*time.Minute {
		return
	}
	s.mu.Lock()
	busy := s.state == StateDownloading || s.state == StateReady || s.state == StateApplying
	s.mu.Unlock()
	if busy {
		return
	}
	_ = os.RemoveAll(filepath.Join(d, "dl"))
	_ = os.RemoveAll(filepath.Join(d, "stage"))
	_ = os.Remove(filepath.Join(d, planFile))
	_ = os.Remove(filepath.Join(d, aliveFile))
}
