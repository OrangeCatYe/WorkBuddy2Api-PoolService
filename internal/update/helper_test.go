package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// 测试用的「假 poolsvc」：直接复制测试二进制本身，末尾附加一段标记（版本号、是否启动即失败），
// 再按参数行为：
//
//	-helper-listen ADDR   监听 ADDR，提供 /healthz（报告自己的版本）和 /quit
//	update-apply -plan X  就是真实的更新程序入口
//	version               打印自己的版本
//
// 这样 Apply / Service.Apply 的替换、拉起、探活、回滚都用真实进程验证，而不是只测函数。
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-helper-listen":
			os.Exit(runHelperServer(os.Args[2]))
		case "update-apply":
			os.Exit(RunApply(os.Args[2:]))
		case "version":
			v, _ := helperMeta()
			fmt.Println(v)
			return
		}
	}
	os.Exit(m.Run())
}

var verRe = regexp.MustCompile(`\nPSVER=(\S+)\n`)

// helperMeta 读自己可执行文件末尾的标记。
func helperMeta() (ver string, fail bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return "", false
	}
	if len(b) > 512 {
		b = b[len(b)-512:]
	}
	if m := verRe.FindSubmatch(b); m != nil {
		ver = string(m[1])
	}
	return ver, bytes.Contains(b, []byte("\nPSFAIL=1\n"))
}

func runHelperServer(addr string) int {
	ver, fail := helperMeta()
	if fail {
		fmt.Println("helper: 按标记模拟启动即失败")
		return 3
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 与空账号池时的真实行为一致：503 也算服务正常
		json.NewEncoder(w).Encode(map[string]any{"service": "poolservice", "version": ver})
	})
	mux.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bye"))
		go func() { time.Sleep(50 * time.Millisecond); os.Exit(0) }()
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Println("helper: listen:", err)
		return 4
	}
	_ = http.Serve(ln, mux)
	return 0
}

// makeHelper 生成一个版本为 ver 的假 poolsvc。
func makeHelper(t testing.TB, dst, ver string, fail bool) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	trailer := "\nPSVER=" + ver + "\n"
	if fail {
		trailer += "PSFAIL=1\n"
	}
	if _, err := out.WriteString(trailer); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func freeAddr(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// healthVersion 查询假服务报告的版本；连不上返回空串。
func healthVersion(addr string) string {
	c := &http.Client{Timeout: time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var b struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&b)
	return b.Version
}

func waitHealth(t testing.TB, addr, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if healthVersion(addr) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s 内 %s 没有报告版本 %s（当前 %q）", d, addr, want, healthVersion(addr))
}

// quitServer 让 addr 上的假服务退出（测试收尾，避免残留进程）。
func quitServer(addr string) {
	c := &http.Client{Timeout: time.Second}
	if resp, err := c.Get("http://" + addr + "/quit"); err == nil {
		resp.Body.Close()
	}
}

// startOld 启动「旧版本」假服务，并在后台回收它（类 Unix 上不回收会变成僵尸进程，kill -0 仍然成功）。
func startOld(t testing.TB, exe, addr string) (pid int, kill func()) {
	t.Helper()
	cmd := execCommand(exe, "-helper-listen", addr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	waitHealth(t, addr, versionOf(exe), 10*time.Second)
	t.Cleanup(func() { quitServer(addr); _ = cmd.Process.Kill() })
	return cmd.Process.Pid, func() { _ = cmd.Process.Kill(); <-done }
}

func versionOf(exe string) string {
	b, _ := os.ReadFile(exe)
	if len(b) > 512 {
		b = b[len(b)-512:]
	}
	if m := verRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}
