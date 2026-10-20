package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installLayout 模拟用户的安装目录与暂存目录。
type installLayout struct {
	inst, stage, upd string
	exe, stageExe    string
	addr             string
}

func newLayout(t *testing.T, oldVer, newVer string, newFails bool) installLayout {
	t.Helper()
	root := t.TempDir()
	l := installLayout{
		inst: filepath.Join(root, "inst"), stage: filepath.Join(root, "upd", "stage", newVer), upd: filepath.Join(root, "upd"),
		addr: freeAddr(t),
	}
	exe := ExeName("poolsvc")
	l.exe, l.stageExe = filepath.Join(l.inst, exe), filepath.Join(l.stage, exe)
	makeHelper(t, l.exe, oldVer, false)
	makeHelper(t, l.stageExe, newVer, newFails)

	// 用户数据与文件：更新绝不能动 config.json；start 脚本用户可能改过；README 应被覆盖；LICENSE 原本没有。
	writeFile(t, filepath.Join(l.inst, "config.json"), `{"admin_key":"keep-me"}`)
	writeFile(t, filepath.Join(l.inst, "start.bat"), "用户自己改过的启动脚本")
	writeFile(t, filepath.Join(l.inst, "README.md"), "旧 README")
	writeFile(t, filepath.Join(l.inst, "data", "workbuddy", "state.json"), `{"credits":123}`)
	writeFile(t, filepath.Join(l.stage, "start.bat"), "官方默认启动脚本")
	writeFile(t, filepath.Join(l.stage, "README.md"), "新 README")
	writeFile(t, filepath.Join(l.stage, "LICENSE"), "MIT")
	return l
}

func (l installLayout) plan(oldPid int, from, to string) *Plan {
	return &Plan{
		Schema: 1, PID: oldPid, ExePath: l.exe, MainRel: ExeName("poolsvc"),
		Args: []string{"-helper-listen", l.addr}, Cwd: l.inst, ProbeAddr: l.addr,
		FromVersion: from, ToVersion: to, StageDir: l.stage, UpdateDir: l.upd,
		ExtraBackups: []string{filepath.Join(l.inst, "config.json"), filepath.Join(l.inst, "data", "workbuddy", "state.json")},
		GraceSeconds: 20,
		Files: []PlanFile{
			{Rel: ExeName("poolsvc"), Mode: ModeReplace},
			{Rel: "README.md", Mode: ModeReplace},
			{Rel: "LICENSE", Mode: ModeReplace},
			{Rel: "start.bat", Mode: ModeIfMissing},
		},
	}
}

var quiet = func(string, ...any) {}

// 成功路径：旧进程退出 → 备份 → 替换 → 新版本拉起并通过探活。
func TestApplySuccess(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", false)
	pid, kill := startOld(t, l.exe, l.addr)
	t.Cleanup(func() { quitServer(l.addr) })
	go func() { time.Sleep(300 * time.Millisecond); kill() }() // 模拟旧服务优雅退出

	res := Apply(l.plan(pid, "v1.0.0", "v1.0.1"), quiet)
	if !res.OK || res.RolledBack {
		t.Fatalf("应成功: %+v", res)
	}
	waitHealth(t, l.addr, "v1.0.1", 5*time.Second)
	if versionOf(l.exe) != "v1.0.1" {
		t.Error("主程序应已替换为新版本")
	}
	if readFile(t, filepath.Join(l.inst, "README.md")) != "新 README" {
		t.Error("README 应被覆盖")
	}
	if readFile(t, filepath.Join(l.inst, "LICENSE")) != "MIT" {
		t.Error("原本没有的 LICENSE 应补上")
	}
	if readFile(t, filepath.Join(l.inst, "start.bat")) != "用户自己改过的启动脚本" {
		t.Error("用户改过的启动脚本不得被覆盖")
	}
	if readFile(t, filepath.Join(l.inst, "config.json")) != `{"admin_key":"keep-me"}` {
		t.Error("config.json 绝不能被改动")
	}
	if readFile(t, filepath.Join(l.inst, "data", "workbuddy", "state.json")) != `{"credits":123}` {
		t.Error("state.json 绝不能被改动")
	}
	for _, p := range []string{".new"} {
		if m, _ := filepath.Glob(filepath.Join(l.inst, "*"+p)); len(m) > 0 {
			t.Errorf("不应残留临时文件: %v", m)
		}
	}

	// 备份：旧主程序、被覆盖的 README，以及 config/state 的安全副本。
	bk, _ := filepath.Glob(filepath.Join(l.upd, "backup", "v1.0.0-*"))
	if len(bk) != 1 {
		t.Fatalf("应有一份备份: %v", bk)
	}
	if versionOf(filepath.Join(bk[0], "files", ExeName("poolsvc"))) != "v1.0.0" {
		t.Error("备份里应是旧版本主程序")
	}
	if readFile(t, filepath.Join(bk[0], "files", "README.md")) != "旧 README" {
		t.Error("备份里应是旧 README")
	}
	if !exists(filepath.Join(bk[0], "extra", "0-config.json")) || !exists(filepath.Join(bk[0], "extra", "1-state.json")) {
		t.Error("config.json / state.json 应有安全副本")
	}
	if exists(filepath.Join(bk[0], "files", "start.bat")) {
		t.Error("未被改动的文件不需要备份")
	}
}

// 旧进程不肯自己退出：超过宽限期后被强制结束，更新照常完成。
func TestApplyForceKillsStuckOldProcess(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", false)
	pid, _ := startOld(t, l.exe, l.addr)
	t.Cleanup(func() { quitServer(l.addr) })
	p := l.plan(pid, "v1.0.0", "v1.0.1")
	p.GraceSeconds = 1

	start := time.Now()
	res := Apply(p, quiet)
	if !res.OK {
		t.Fatalf("强制结束旧进程后应能完成更新: %+v", res)
	}
	if d := time.Since(start); d < time.Second {
		t.Errorf("应先等满宽限期再强杀，实际只等了 %s", d)
	}
	waitHealth(t, l.addr, "v1.0.1", 5*time.Second)
}

// 新版本一启动就退出：自动回滚，旧版本被重新拉起，所有文件恢复原状。
func TestApplyRollsBackWhenNewVersionCrashes(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", true)
	pid, kill := startOld(t, l.exe, l.addr)
	t.Cleanup(func() { quitServer(l.addr) })
	go func() { time.Sleep(200 * time.Millisecond); kill() }()

	res := Apply(l.plan(pid, "v1.0.0", "v1.0.1"), quiet)
	if res.OK || !res.RolledBack {
		t.Fatalf("应回滚: %+v", res)
	}
	if !strings.Contains(res.Message, "服务已恢复运行") {
		t.Errorf("回滚后旧服务应被重新拉起: %q", res.Message)
	}
	waitHealth(t, l.addr, "v1.0.0", 5*time.Second)
	if versionOf(l.exe) != "v1.0.0" {
		t.Error("主程序应恢复为旧版本")
	}
	if readFile(t, filepath.Join(l.inst, "README.md")) != "旧 README" {
		t.Error("README 应恢复")
	}
	if exists(filepath.Join(l.inst, "LICENSE")) {
		t.Error("更新时新增的 LICENSE 应被移除")
	}
	if readFile(t, filepath.Join(l.inst, "config.json")) != `{"admin_key":"keep-me"}` {
		t.Error("config.json 不得被改动")
	}
}

// 替换到一半失败（暂存里缺文件）：已替换的全部还原，旧服务被重新拉起。
func TestApplyRollsBackWhenReplaceFailsMidway(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", false)
	pid, kill := startOld(t, l.exe, l.addr)
	t.Cleanup(func() { quitServer(l.addr) })
	if err := os.Remove(filepath.Join(l.stage, "LICENSE")); err != nil { // 计划里有、暂存里没有
		t.Fatal(err)
	}
	go func() { time.Sleep(200 * time.Millisecond); kill() }()

	res := Apply(l.plan(pid, "v1.0.0", "v1.0.1"), quiet)
	if res.OK || !res.RolledBack {
		t.Fatalf("应回滚: %+v", res)
	}
	waitHealth(t, l.addr, "v1.0.0", 5*time.Second)
	if versionOf(l.exe) != "v1.0.0" || readFile(t, filepath.Join(l.inst, "README.md")) != "旧 README" {
		t.Error("替换失败后必须整体还原（包括已经替换过的文件）")
	}
}

// 新版本能启动但报告的版本不对（例如包装错了）：同样视为失败并回滚。
func TestApplyRollsBackOnVersionMismatch(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.9", false) // 暂存里实际是 1.0.9
	pid, kill := startOld(t, l.exe, l.addr)
	t.Cleanup(func() { quitServer(l.addr) })
	go func() { time.Sleep(200 * time.Millisecond); kill() }()
	p := l.plan(pid, "v1.0.0", "v1.0.1") // 声明的目标版本是 1.0.1

	oldProbe := probeTimeout
	probeTimeout = 6 * time.Second
	t.Cleanup(func() { probeTimeout = oldProbe })
	res := Apply(p, quiet)
	if res.OK || !res.RolledBack {
		t.Fatalf("版本不符不应算成功，应回滚: %+v", res)
	}
	waitHealth(t, l.addr, "v1.0.0", 5*time.Second)
	if versionOf(l.exe) != "v1.0.0" {
		t.Error("版本不符时主程序应恢复为旧版本")
	}
}

// 旧进程已经不存在（比如更新程序启动前它就退出了）：不能卡住。
func TestApplyWithAlreadyExitedOldProcess(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", false)
	pid, kill := startOld(t, l.exe, l.addr)
	kill()
	t.Cleanup(func() { quitServer(l.addr) })
	start := time.Now()
	res := Apply(l.plan(pid, "v1.0.0", "v1.0.1"), quiet)
	if !res.OK {
		t.Fatalf("旧进程已退出时应直接继续: %+v", res)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("不应干等宽限期: %s", time.Since(start))
	}
}

// 托管模式：主进程自己替换文件，不拉起任何进程。
func TestApplyInPlaceReplacesFilesWithoutStartingProcess(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", false)
	res := ApplyInPlace(l.plan(os.Getpid(), "v1.0.0", "v1.0.1"), quiet)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if versionOf(l.exe) != "v1.0.1" || readFile(t, filepath.Join(l.inst, "README.md")) != "新 README" {
		t.Error("文件应已替换")
	}
	if healthVersion(l.addr) != "" {
		t.Error("托管模式不应自己拉起进程")
	}
	// 中途失败要还原。
	l2 := newLayout(t, "v1.0.0", "v1.0.1", false)
	os.Remove(filepath.Join(l2.stage, "LICENSE"))
	res = ApplyInPlace(l2.plan(os.Getpid(), "v1.0.0", "v1.0.1"), quiet)
	if res.OK || !res.RolledBack || versionOf(l2.exe) != "v1.0.0" {
		t.Errorf("失败应整体还原: %+v", res)
	}
}

func TestBackupsArePruned(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", false)
	root := filepath.Join(l.upd, "backup")
	for _, n := range []string{"v0.9.1-20260101-000000", "v0.9.2-20260102-000000", "v0.9.3-20260103-000000", "v0.9.4-20260104-000000"} {
		writeFile(t, filepath.Join(root, n, "files", "x"), "x")
	}
	a := &applier{plan: l.plan(1, "v1.0.0", "v1.0.1"), logf: quiet, existed: map[string]bool{}}
	if err := a.backup(); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(root)
	if len(ents) != keepBackups {
		t.Errorf("只应保留最近 %d 份备份，实际 %d", keepBackups, len(ents))
	}
	if exists(filepath.Join(root, "v0.9.1-20260101-000000")) {
		t.Error("最旧的备份应被清理")
	}
}

func TestPlanRoundTripAndValidation(t *testing.T) {
	l := newLayout(t, "v1.0.0", "v1.0.1", false)
	p := l.plan(1234, "v1.0.0", "v1.0.1")
	path := filepath.Join(l.upd, "plan.json")
	if err := WritePlan(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPlan(path)
	if err != nil || got.PID != 1234 || got.ExePath != l.exe || len(got.Files) != 4 || got.Args[1] != l.addr {
		t.Fatalf("往返不一致: %+v %v", got, err)
	}
	for name, body := range map[string]string{
		"不完整":     `{"schema":1,"pid":1}`,
		"版本号不对":   `{"schema":9,"pid":1,"exe_path":"x","main_rel":"x","stage_dir":"x","update_dir":"x","files":[{"rel":"x"}]}`,
		"不是 JSON": "garbage",
	} {
		writeFile(t, path, body)
		if _, err := LoadPlan(path); err == nil {
			t.Errorf("%s：应被拒绝", name)
		}
	}
}

func TestSameVersion(t *testing.T) {
	if !sameVersion("v1.2.3", "1.2.3") || sameVersion("v1.2.3", "v1.2.4") || !sameVersion("dev", "dev") || sameVersion("dev", "v1.0.0") {
		t.Error("sameVersion 判断有误")
	}
}
