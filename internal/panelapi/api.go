// Package panelapi 面板 REST API（/api/*），与网关共用进程，使用管理密钥鉴权。
package panelapi

import (
	"net/http"
	"sort"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
	"github.com/orangeruan/poolservice/internal/gateway"
	"github.com/orangeruan/poolservice/internal/httpx"
)

// Version 服务版本（构建时 -ldflags 注入）。
var Version = "dev"

// API 面板接口。
type API struct {
	cfg     func() *config.Config
	reg     *provider.Registry
	catalog *router.Catalog
	started time.Time
}

// New 创建。
func New(cfg func() *config.Config, reg *provider.Registry, catalog *router.Catalog) *API {
	return &API{cfg: cfg, reg: reg, catalog: catalog, started: time.Now()}
}

// Register 挂载路由。
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/meta", a.auth(a.meta))
	mux.HandleFunc("GET /api/overview", a.auth(a.overview))
	mux.HandleFunc("GET /api/accounts", a.auth(a.accounts))
	mux.HandleFunc("GET /api/models", a.auth(a.models))
	mux.HandleFunc("POST /api/models/refresh", a.auth(a.refreshModels))
	a.registerProviderRoutes(mux)
}

func (a *API) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := a.cfg().AdminKey
		if key != "" && !httpx.SecretEqual(httpx.BearerToken(r), key) {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

func (a *API) meta(w http.ResponseWriter, r *http.Request) {
	metas := make([]provider.Meta, 0)
	for _, p := range a.reg.List() {
		metas = append(metas, provider.MetaWithCaps(p))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"version":    Version,
		"started_at": a.started,
		"providers":  metas,
		"gateway": map[string]any{
			"model_naming":         a.cfg().Gateway.ModelNaming,
			"credit_in_model_name": a.cfg().Gateway.CreditInModelName,
		},
	})
}

// ProviderSummary 单 Provider 汇总。
type ProviderSummary struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Total     int     `json:"total"`
	Active    int     `json:"active"`
	Cooling   int     `json:"cooling"`
	Paused    int     `json:"paused"`
	Disabled  int     `json:"disabled"`
	Remaining float64 `json:"remaining"`
	Quota     float64 `json:"quota"`
	// Expiring7d 未来 7 天内到期且未用完的积分
	Expiring7d float64 `json:"expiring_7d"`
}

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	horizon := now.Add(7 * 24 * time.Hour)
	var list []ProviderSummary
	var all ProviderSummary
	all.ID, all.Name = "all", "全部"
	for _, p := range a.reg.List() {
		m := p.Meta()
		s := ProviderSummary{ID: m.ID, Name: m.Name}
		for _, acc := range p.Accounts() {
			s.Total++
			switch acc.Status {
			case account.StatusActive:
				s.Active++
			case account.StatusCooling:
				s.Cooling++
			case account.StatusPaused:
				s.Paused++
			default:
				s.Disabled++
			}
			if q := acc.Quota; q != nil && !q.Unlimited {
				s.Remaining += q.Remaining
				s.Quota += q.Total
				for _, pk := range q.Packages {
					if pk.ExpireAt != nil && pk.ExpireAt.After(now) && pk.ExpireAt.Before(horizon) {
						s.Expiring7d += pk.Remaining
					}
				}
			}
		}
		list = append(list, s)
		all.Total += s.Total
		all.Active += s.Active
		all.Cooling += s.Cooling
		all.Paused += s.Paused
		all.Disabled += s.Disabled
		all.Remaining += s.Remaining
		all.Quota += s.Quota
		all.Expiring7d += s.Expiring7d
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"summary": all, "providers": list, "now": now})
}

func (a *API) accounts(w http.ResponseWriter, r *http.Request) {
	want := r.URL.Query().Get("provider")
	out := make([]account.Account, 0)
	for _, p := range a.reg.List() {
		if want != "" && p.Meta().ID != want {
			continue
		}
		out = append(out, p.Accounts()...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return a.reg.Priority(out[i].Provider) < a.reg.Priority(out[j].Provider)
		}
		return out[i].Label < out[j].Label
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (a *API) models(w http.ResponseWriter, r *http.Request) {
	entries := a.catalog.Entries(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"models": gateway.ModelEntries(entries, gateway.NamingOptions{Style: "short"}),
		"errors": a.catalog.Errors(),
	})
}

func (a *API) refreshModels(w http.ResponseWriter, r *http.Request) {
	a.catalog.Refresh(r.Context())
	a.models(w, r)
}
