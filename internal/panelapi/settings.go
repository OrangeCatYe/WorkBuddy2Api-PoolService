package panelapi

import (
	"net/http"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/httpx"
)

// Updater 原子修改并落盘配置（由 app 注入）。
type Updater func(mut func(*config.Config)) error

// registerSettingsRoutes 网关设置：热生效字段（命名风格、倍率后缀）。
func (a *API) registerSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/gateway", a.auth(a.getGateway))
	mux.HandleFunc("PUT /api/settings/gateway", a.auth(a.putGateway))
}

type gatewaySettings struct {
	ModelNaming       string `json:"model_naming"`
	CreditInModelName bool   `json:"credit_in_model_name"`
}

func (a *API) getGateway(w http.ResponseWriter, _ *http.Request) {
	g := a.cfg().Gateway
	httpx.WriteJSON(w, http.StatusOK, gatewaySettings{ModelNaming: g.ModelNaming, CreditInModelName: g.CreditInModelName})
}

func (a *API) putGateway(w http.ResponseWriter, r *http.Request) {
	if a.update == nil {
		writeErr(w, http.StatusNotImplemented, "配置不可写")
		return
	}
	var in gatewaySettings
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	switch in.ModelNaming {
	case "short", "full", "both":
	default:
		writeErr(w, http.StatusBadRequest, "model_naming 只能是 short / full / both")
		return
	}
	if err := a.update(func(c *config.Config) {
		c.Gateway.ModelNaming = in.ModelNaming
		c.Gateway.CreditInModelName = in.CreditInModelName
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	a.getGateway(w, r)
}
