//go:build !windows

package sysproc

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// AttachEnv 类 Unix 不需要，保持与 Windows 一致的名字。
const AttachEnv = "POOLSVC_CONSOLE=unused"

// HasConsoleOutput stderr 是否连着终端。
func HasConsoleOutput() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// StdoutIsConsole 标准输出是否连着终端。
func StdoutIsConsole() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// DetachConsole 类 Unix 无需处理。
func DetachConsole() {}

// AttachConsole 类 Unix 无需处理。
func AttachConsole() bool { return false }

func startDetached(cmd *exec.Cmd, _ bool) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// WaitPID 等待进程退出。返回 true 表示已退出（或本来就不存在），false 表示超时仍在运行。
func WaitPID(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// KillPID 强制结束进程。
func KillPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// UnderSupervisor 是否由 systemd 等服务管理器托管。托管时进程退出会被拉起，
// 更新程序若再自己拉起新进程就会抢端口，而且子进程会随服务的 cgroup 一并被清理。
func UnderSupervisor() bool { return os.Getenv("INVOCATION_ID") != "" }
