// Package app 装配：加载配置、注册 Provider、挂载 HTTP 路由、管理生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
	"github.com/orangeruan/poolservice/internal/gateway"
	"github.com/orangeruan/poolservice/internal/httpx"
	"github.com/orangeruan/poolservice/internal/panelapi"
	"github.com/orangeruan/poolservice/internal/providers/mock"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy"
	"github.com/orangeruan/poolservice/internal/webui"
)

// App 应用实例。
type App struct {
	cfg     atomic.Pointer[config.Config]
	reg     *provider.Registry
	catalog *router.Catalog
	server  *http.Server
}

type toggle struct {
	Enabled bool `json:"enabled"`
}

// New 根据配置装配应用。
func New(cfg *config.Config) (*App, error) {
	a := &App{reg: provider.NewRegistry()}
	a.cfg.Store(cfg)

	// WorkBuddy：未配置时按默认值启用（providers.workbuddy.enabled=false 关闭）。
	wbs, err := workbuddy.ParseSettings(cfg.Providers[workbuddy.ID], cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if wbs.Enabled {
		wb, err := workbuddy.New(wbs)
		if err != nil {
			return nil, err
		}
		if err := a.reg.Register(wb); err != nil {
			return nil, err
		}
	}

	var mk toggle
	if err := cfg.ProviderConfig("mock", &mk); err != nil {
		return nil, fmt.Errorf("providers.mock: %w", err)
	}
	if mk.Enabled {
		if err := a.reg.Register(mock.New()); err != nil {
			return nil, err
		}
	}

	a.catalog = router.NewCatalog(a.reg, cfg.Gateway.CatalogTTL.Duration)
	current := func() *config.Config { return a.cfg.Load() }

	mux := http.NewServeMux()
	gateway.New(current, a.reg, a.catalog, nil).Register(mux)

	apiMux := http.NewServeMux()
	panelapi.New(current, a.reg, a.catalog).Register(apiMux)
	mux.Handle("/api/", httpx.SecurityHeaders(apiMux))

	mux.Handle("GET /panel/", http.RedirectHandler("/", http.StatusMovedPermanently))
	mux.Handle("/", httpx.SecurityHeaders(webui.Handler()))

	a.server = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       300 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return a, nil
}

// Registry 暴露注册表（测试与扩展用）。
func (a *App) Registry() *provider.Registry { return a.reg }

// Run 启动 Provider 后台任务与 HTTP 服务，直到 ctx 取消。
// 退出顺序：先优雅关闭 HTTP（等在途请求结束），再逆序停止 Provider 并落盘。
func (a *App) Run(ctx context.Context) error {
	var started []provider.Starter
	defer func() {
		for i := len(started) - 1; i >= 0; i-- {
			started[i].Stop()
		}
	}()
	for _, p := range a.reg.List() {
		if s, ok := p.(provider.Starter); ok {
			if err := s.Start(ctx); err != nil {
				return fmt.Errorf("start provider %s: %w", p.Meta().ID, err)
			}
			started = append(started, s)
		}
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("[app] listening on http://%s", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.server.Shutdown(shutdownCtx)
}
