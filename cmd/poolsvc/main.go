// Command poolsvc 多平台账号池网关主程序。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/orangeruan/poolservice/internal/app"
	"github.com/orangeruan/poolservice/internal/config"
)

func main() {
	cfgPath := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	cfg, created, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
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
