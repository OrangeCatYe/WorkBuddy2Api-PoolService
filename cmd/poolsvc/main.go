// Command poolsvc 多平台账号池网关主程序。
//
//	poolsvc [-config config.json] [-background]   启动服务
//	poolsvc version                               打印版本号
//	poolsvc update-apply -plan <file>             软件更新的接力程序（由主程序拉起，用户无需手动运行）
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/orangeruan/poolservice/internal/app"
	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/logfile"
	"github.com/orangeruan/poolservice/internal/panelapi"
	"github.com/orangeruan/poolservice/internal/sysproc"
	"github.com/orangeruan/poolservice/internal/update"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-version", "--version":
			fmt.Println(panelapi.Version)
			return
		case "update-apply":
			os.Exit(update.RunApply(os.Args[2:]))
		}
	}

	// 更新程序用新控制台拉起本进程时，Go 默认把标准流接到 NUL；先接回自己的控制台，日志才看得见。
	if os.Getenv("POOLSVC_CONSOLE") == "attach" {
		sysproc.AttachConsole()
		os.Unsetenv("POOLSVC_CONSOLE") // 不让它继续传给后续子进程
	}

	cfgPath := flag.String("config", "config.json", "配置文件路径")
	background := flag.Bool("background", false, "后台运行：释放控制台窗口，日志写入 data/logs/poolsvc.log（开机自启用）")
	flag.Parse()

	cfg, created, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if *background {
		sysproc.DetachConsole()
	}
	closeLog := setupLogging(cfg, *background)
	defer closeLog()

	if created {
		log.Printf("[app] 已生成默认配置 %s，面板管理密钥：%s", *cfgPath, cfg.AdminKey)
	}

	a, err := app.New(cfg)
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil {
		log.Fatalf("run: %v", err)
	}
}

// setupLogging 没有控制台可看日志时（开机自启、后台运行）写文件，否则这些日志会直接丢失。
// 有控制台时保持原样只输出到控制台。
func setupLogging(cfg *config.Config, background bool) (closeFn func()) {
	if !background && sysproc.HasConsoleOutput() {
		return func() {}
	}
	dir := cfg.DataDir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	w, err := logfile.Open(filepath.Join(dir, "logs", "poolsvc.log"), 5<<20, 3)
	if err != nil {
		return func() {}
	}
	var out io.Writer = w
	if !background {
		// 没有终端但 stderr 仍可用（如 systemd 的 journal）：两边都写。
		out = logfile.Tee(w, os.Stderr)
	}
	log.SetOutput(out)
	return func() { _ = w.Close() }
}
