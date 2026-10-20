//go:build windows

package sysproc

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	createNewConsole      = 0x00000010
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
	createBreakawayJob    = 0x01000000

	errorInvalidParameter syscall.Errno = 87 // OpenProcess：进程不存在
)

// AttachEnv 为 attach 时，进程启动后把标准输入输出接到自己的控制台。
// 更新程序用 CREATE_NEW_CONSOLE 拉起新版本时，Go 会给子进程传 NUL 作为标准流，
// 窗口虽然有了但日志进不去，所以由子进程自己打开 CONOUT$。
const AttachEnv = "POOLSVC_CONSOLE=attach"

// StdoutIsConsole 当前标准输出是否是控制台（而不是重定向到文件 / 管道 / NUL）。
func StdoutIsConsole() bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(os.Stdout.Fd()), &mode) == nil
}

// HasConsoleOutput 同 StdoutIsConsole；类 Unix 上判断是否 tty，这里保持同名接口。
func HasConsoleOutput() bool { return StdoutIsConsole() }

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	procFreeCons = kernel32.NewProc("FreeConsole")
)

// DetachConsole 释放控制台，窗口随之关闭（后台运行模式用）。
func DetachConsole() { _, _, _ = procFreeCons.Call() }

// AttachConsole 把标准流接到当前进程自己的控制台，并返回是否成功。
func AttachConsole() bool {
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err == nil {
		os.Stdin = in
	}
	os.Stdout, os.Stderr = out, out
	log.SetOutput(out)
	return true
}

func startDetached(cmd *exec.Cmd, visible bool) error {
	flags := uint32(createNewProcessGroup)
	if visible {
		flags |= createNewConsole
		cmd.Env = append(cmd.Env, AttachEnv)
	} else {
		flags |= createNoWindow
	}
	// 先尝试脱离作业对象（终端 / 服务管理器可能把子进程算进同一个 job，随父进程一起被杀）；
	// job 不允许脱离时 CreateProcess 会失败，再退回不带该标志的方式。
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | createBreakawayJob, HideWindow: !visible}
	if err := cmd.Start(); err == nil {
		return nil
	}
	retry := exec.Command(cmd.Path, cmd.Args[1:]...)
	retry.Dir, retry.Env = cmd.Dir, cmd.Env
	retry.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: !visible}
	if err := retry.Start(); err != nil {
		return err
	}
	cmd.Process = retry.Process
	return nil
}

// WaitPID 等待进程退出。返回 true 表示已退出（或本来就不存在），false 表示超时仍在运行。
func WaitPID(pid int, timeout time.Duration) bool {
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, errorInvalidParameter)
	}
	defer syscall.CloseHandle(h)
	ev, err := syscall.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	return err == nil && ev == syscall.WAIT_OBJECT_0
}

// KillPID 强制结束进程。
func KillPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// UnderSupervisor 是否由服务管理器托管。Windows 不做这种判断。
func UnderSupervisor() bool { return false }
