package update

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/sysproc"
)

const (
	defaultGrace   = 30 * time.Second
	killWait       = 10 * time.Second
	keepBackups    = 3
	maxExtraBackup = 50 << 20
)

// 探活等待时间：新版本最长多久内必须起来；回滚后旧版本最长多久内必须起来。测试里会缩短。
var (
	probeTimeout  = 45 * time.Second
	fallbackProbe = 30 * time.Second
)

// RunApply 是 `poolsvc update-apply -plan <file>` 子命令的入口：它由主程序从新版本的安装包里
// 拉起，在旧进程退出后完成替换并重启。返回进程退出码。
func RunApply(args []string) int {
	fs := flag.NewFlagSet("update-apply", flag.ContinueOnError)
	planPath := fs.String("plan", "", "更新计划文件")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	plan, err := LoadPlan(*planPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "update-apply:", err)
		return 2
	}
	_ = os.MkdirAll(plan.UpdateDir, 0o755)
	logPath := filepath.Join(plan.UpdateDir, applyLog)
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 1<<20 {
		_ = os.Remove(logPath)
	}
	lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	logf := func(format string, a ...any) {
		line := time.Now().Format("2006-01-02 15:04:05 ") + fmt.Sprintf(format, a...) + "\n"
		if lf != nil {
			lf.WriteString(line)
		}
		fmt.Print(line)
	}
	if lf != nil {
		defer lf.Close()
	}
	// 通知主程序「更新程序已成功拉起」。没有这个标记，主程序会认为拉起失败（例如被安全软件拦截）
	// 而放弃退出，旧服务继续运行，不会出现「服务停了更新程序也没起来」的局面。
	_ = os.WriteFile(filepath.Join(plan.UpdateDir, aliveFile), []byte(strconv.Itoa(os.Getpid())), 0o644)
	logf("更新程序启动：%s → %s", plan.FromVersion, plan.ToVersion)

	res := Apply(plan, logf)
	if err := WriteResult(plan.UpdateDir, res); err != nil {
		logf("写结果文件失败: %v", err)
	}
	logf("结束：%s", res.Message)
	_ = os.Remove(*planPath)
	_ = os.Remove(filepath.Join(plan.UpdateDir, aliveFile))
	_ = os.RemoveAll(plan.StageDir) // Windows 上更新程序自己的 exe 在里面，删不掉，留给主程序稍后清理
	if res.OK {
		return 0
	}
	return 1
}

// Apply 执行更新：等旧进程退出 → 备份 → 替换 → 拉起新版本 → 探活；
// 新版本起不来就恢复备份并重新拉起旧版本。
func Apply(plan *Plan, logf func(string, ...any)) Result {
	a := &applier{plan: plan, logf: logf, existed: map[string]bool{}}
	res := Result{From: plan.FromVersion, To: plan.ToVersion, At: time.Now()}

	grace := time.Duration(plan.GraceSeconds) * time.Second
	if grace <= 0 {
		grace = defaultGrace
	}
	a.logf("等待旧进程 %d 自行退出（最多 %s）", plan.PID, grace)
	if !sysproc.WaitPID(plan.PID, grace) {
		a.logf("旧进程未在期限内退出，强制结束")
		_ = sysproc.KillPID(plan.PID)
		if !sysproc.WaitPID(plan.PID, killWait) {
			res.Message = "旧进程无法结束，已放弃更新，请手动重启服务"
			return res
		}
	}

	// 从这里开始旧服务已经停了：之后任何失败路径都必须把服务重新拉起来。
	if err := a.backup(); err != nil {
		a.logf("备份失败: %v", err)
		res.Message = "备份失败，未改动任何文件：" + err.Error()
		a.fallbackStart(&res)
		return res
	}
	if err := a.replace(); err != nil {
		a.logf("替换失败: %v，回滚", err)
		a.restore()
		res.RolledBack = true
		res.Message = "替换文件失败，已回滚：" + err.Error()
		a.fallbackStart(&res)
		return res
	}
	if err := a.startAndProbe(plan.ToVersion, probeTimeout); err != nil {
		a.logf("新版本启动失败: %v，回滚", err)
		a.stopNew()
		a.restore()
		res.RolledBack = true
		res.Message = "新版本未能正常启动，已自动回滚到旧版本：" + err.Error()
		a.fallbackStart(&res)
		return res
	}
	res.OK = true
	res.Message = "已更新到 " + plan.ToVersion
	a.logf("%s", res.Message)
	return res
}

// ApplyInPlace 由服务管理器（systemd 等）托管时使用：主进程自己完成备份和替换，
// 不拉起任何进程，退出后由服务管理器按其策略重启。类 Unix 允许替换正在运行的可执行文件。
func ApplyInPlace(plan *Plan, logf func(string, ...any)) Result {
	a := &applier{plan: plan, logf: logf, existed: map[string]bool{}}
	res := Result{From: plan.FromVersion, To: plan.ToVersion, At: time.Now()}
	if err := a.backup(); err != nil {
		res.Message = "备份失败，未改动任何文件：" + err.Error()
		return res
	}
	if err := a.replace(); err != nil {
		a.restore()
		res.RolledBack = true
		res.Message = "替换文件失败，已回滚：" + err.Error()
		return res
	}
	res.OK = true
	res.Message = "文件已替换为 " + plan.ToVersion + "，等待服务管理器重启服务"
	return res
}

type applier struct {
	plan      *Plan
	logf      func(string, ...any)
	backupDir string
	existed   map[string]bool // 本次会改动的文件：更新前是否已存在
	touched   []string        // 已改动的文件（按顺序），回滚用
	proc      *os.Process
	exited    chan struct{}
}

// backup 备份将被改动的文件，以及 config.json、state.json 等用户文件的安全副本。
func (a *applier) backup() error {
	p := a.plan
	a.backupDir = filepath.Join(p.UpdateDir, "backup", fmt.Sprintf("%s-%s", sanitize(p.FromVersion), time.Now().Format("20060102-150405")))
	for _, f := range p.Files {
		target := p.target(f.Rel)
		_, err := os.Stat(target)
		exists := err == nil
		if f.Mode == ModeIfMissing && exists {
			continue
		}
		a.existed[f.Rel] = exists
		if exists {
			if err := copyFile(target, filepath.Join(a.backupDir, "files", filepath.FromSlash(f.Rel))); err != nil {
				return fmt.Errorf("备份 %s: %w", f.Rel, err)
			}
		}
	}
	for i, src := range p.ExtraBackups {
		fi, err := os.Stat(src)
		if err != nil || fi.Size() > maxExtraBackup {
			continue
		}
		if err := copyFile(src, filepath.Join(a.backupDir, "extra", fmt.Sprintf("%d-%s", i, filepath.Base(src)))); err != nil {
			a.logf("额外备份 %s 失败（忽略）: %v", src, err)
		}
	}
	a.pruneBackups()
	a.logf("已备份到 %s", a.backupDir)
	return nil
}

// pruneBackups 只保留最近几份备份。
func (a *applier) pruneBackups() {
	root := filepath.Join(a.plan.UpdateDir, "backup")
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keepBackups {
		_ = os.RemoveAll(filepath.Join(root, names[0]))
		names = names[1:]
	}
}

// replace 把暂存目录里的文件放到安装位置。
func (a *applier) replace() error {
	for _, f := range a.plan.Files {
		if _, tracked := a.existed[f.Rel]; !tracked { // IfMissing 且目标已存在：不动
			continue
		}
		src := filepath.Join(a.plan.StageDir, filepath.FromSlash(f.Rel))
		if err := installFile(src, a.plan.target(f.Rel)); err != nil {
			return fmt.Errorf("%s: %w", f.Rel, err)
		}
		a.touched = append(a.touched, f.Rel)
		a.logf("已替换 %s", f.Rel)
	}
	return nil
}

// restore 按相反顺序把已改动的文件恢复原状：原来存在的从备份还原，原来没有的删除。
func (a *applier) restore() {
	for i := len(a.touched) - 1; i >= 0; i-- {
		rel := a.touched[i]
		target := a.plan.target(rel)
		if a.existed[rel] {
			if err := installFile(filepath.Join(a.backupDir, "files", filepath.FromSlash(rel)), target); err != nil {
				a.logf("还原 %s 失败: %v（备份保留在 %s）", rel, err, a.backupDir)
			}
		} else {
			_ = os.Remove(target)
		}
	}
	a.touched = nil
}

// start 按计划拉起主程序并监视它是否退出。
func (a *applier) start() error {
	proc, err := sysproc.Start(sysproc.StartOptions{Exe: a.plan.ExePath, Args: a.plan.Args, Dir: a.plan.Cwd, Visible: a.plan.Visible})
	if err != nil {
		return err
	}
	a.proc = proc
	a.exited = make(chan struct{})
	ch := a.exited
	go func() { _, _ = proc.Wait(); close(ch) }()
	a.logf("已拉起进程 %d（新控制台窗口: %v）", proc.Pid, a.plan.Visible)
	return nil
}

func (a *applier) startAndProbe(want string, timeout time.Duration) error {
	if err := a.start(); err != nil {
		return fmt.Errorf("无法启动: %w", err)
	}
	return a.probe(want, timeout)
}

// stopNew 结束刚拉起但没通过探活的新进程，并等它真正退出（Windows 上否则无法还原 exe）。
func (a *applier) stopNew() {
	if a.proc == nil {
		return
	}
	select {
	case <-a.exited:
	default:
		_ = a.proc.Kill()
		select {
		case <-a.exited:
		case <-time.After(killWait):
			a.logf("新进程没有及时退出")
		}
	}
	a.proc = nil
}

// fallbackStart 回滚后（或备份失败时）把服务重新拉起来，并把结果写进说明。
func (a *applier) fallbackStart(res *Result) {
	a.logf("重新拉起原版本")
	if err := a.startAndProbe(a.plan.FromVersion, fallbackProbe); err != nil {
		a.logf("原版本也没有起来: %v", err)
		res.Message += "；原版本也没能自动启动，请手动启动服务（" + err.Error() + "）"
		return
	}
	res.Message += "；服务已恢复运行"
}

// probe 探活：进程持续存活，且 /healthz 连续多次返回期望的版本。
// 不要求 200：账号池为空时 healthz 返回 503，但服务本身是正常的。
func (a *applier) probe(want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var streak int
	var since time.Time
	for time.Now().Before(deadline) {
		select {
		case <-a.exited:
			return errors.New("新进程启动后立即退出")
		default:
		}
		if v, err := a.health(); err == nil && sameVersion(v, want) {
			if streak == 0 {
				since = time.Now()
			}
			streak++
			if streak >= 3 && time.Since(since) >= 2*time.Second {
				return nil
			}
		} else {
			streak = 0
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("%s 内没有探测到版本 %s 的正常响应", timeout, want)
}

var probeClient = &http.Client{
	Timeout:   2 * time.Second,
	Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext},
}

// health 读取 /healthz，返回服务报告的版本号。
func (a *applier) health() (string, error) {
	resp, err := probeClient.Get("http://" + a.plan.ProbeAddr + "/healthz")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Service string `json:"service"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return "", err
	}
	if body.Service != "poolservice" {
		return "", errors.New("端口上不是本服务")
	}
	return body.Version, nil
}

func sameVersion(a, b string) bool {
	va, ok1 := ParseVersion(a)
	vb, ok2 := ParseVersion(b)
	if ok1 && ok2 {
		return va == vb
	}
	return a == b
}

// ---- 文件操作 ----

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}

// copyFile 普通复制（备份用）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// installFile 先在目标目录写 dst.new 再改名覆盖，目标不会出现写了一半的文件。
// 改名遇到文件被占用（常见于杀毒软件正在扫描刚写入的 exe）时按退避重试。
func installFile(src, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	var err error
	for i := 0; i < 10; i++ {
		if err = os.Rename(tmp, dst); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 250 * time.Millisecond)
	}
	_ = os.Remove(tmp)
	return err
}
