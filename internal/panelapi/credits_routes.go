package panelapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/credits"
	"github.com/orangeruan/poolservice/internal/httpx"
)

// SetCredits 注入积分服务。
func (a *API) SetCredits(s *credits.Service) { a.credits = s }

func (a *API) registerCreditsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/credits", a.auth(a.creditsSnapshot))
	mux.HandleFunc("POST /api/credits/refresh", a.auth(a.creditsRefresh))
	mux.HandleFunc("GET /api/credits/decisions", a.auth(a.creditsDecisions))
	mux.HandleFunc("PUT /api/credits/strategy", a.auth(a.creditsStrategy))
}

func (a *API) creditsSnapshot(w http.ResponseWriter, r *http.Request) {
	if a.credits == nil {
		writeErr(w, http.StatusNotImplemented, "积分服务不可用")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a.credits.Fresh(r.Context(), 20*time.Second))
}

func (a *API) creditsRefresh(w http.ResponseWriter, r *http.Request) {
	if a.credits == nil {
		writeErr(w, http.StatusNotImplemented, "积分服务不可用")
		return
	}
	a.credits.Refresh(r.Context())
	httpx.WriteJSON(w, http.StatusOK, a.credits.Snapshot())
}

func (a *API) creditsDecisions(w http.ResponseWriter, r *http.Request) {
	if a.credits == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"decisions": []any{}})
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 {
		n = 50
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"decisions": a.credits.Decisions(n)})
}

func (a *API) creditsStrategy(w http.ResponseWriter, r *http.Request) {
	if a.update == nil || a.credits == nil {
		writeErr(w, http.StatusNotImplemented, "配置不可写")
		return
	}
	var in config.CreditsConfig
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Strategy != "waste_first" && in.Strategy != "off" {
		writeErr(w, http.StatusBadRequest, "strategy 只能是 waste_first / off")
		return
	}
	in.Normalize()
	if err := a.update(func(c *config.Config) { c.Credits = in }); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	a.credits.Refresh(r.Context())
	httpx.WriteJSON(w, http.StatusOK, a.credits.Snapshot())
}
