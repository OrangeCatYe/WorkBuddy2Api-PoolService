// Package sysproc 平台相关的进程与控制台操作：等待 / 结束进程、脱离父进程启动子进程、
// 判断是否挂着可见控制台。更新程序和主程序共用，避免到处写 build tag。
package sysproc

import (
	"os"
	"os/exec"
)

// StartOptions 描述如何拉起一个脱离当前进程的子进程。
type StartOptions struct {
	Exe  string
	Args []string
	Dir  string
	// Visible 为 true 时给子进程一个新的可见控制台窗口（用户原本就是在控制台里运行的）；
	// 否则不创建窗口（开机自启、后台运行）。仅 Windows 有区别，类 Unix 总是新会话。
	Visible bool
	// Env 追加的环境变量，形如 KEY=VALUE。
	Env []string
}

// Start 拉起子进程并立即返回，不等待它结束。子进程不会随当前进程退出而被终止。
func Start(o StartOptions) (*os.Process, error) {
	cmd := exec.Command(o.Exe, o.Args...)
	cmd.Dir = o.Dir
	cmd.Env = append(os.Environ(), o.Env...)
	if err := startDetached(cmd, o.Visible); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}
