package panelapi

import (
	"encoding/json"
	"net/http"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/httpx"
)

func (a *API) registerProviderSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/providers/{pid}/settings", a.auth(a.getProviderSettings))
	mux.HandleFunc("PUT /api/providers/{pid}/settings", a.auth(a.putProviderSettings))
}

type providerSettingsOut struct {
	Fields         []provider.SettingField `json:"fields"`
	Values         map[string]any          `json:"values"`
	SecretsSet     []string                `json:"secrets_set"`
	PendingRestart []string                `json:"pending_restart"`
}

func (a *API) settingsOf(w http.ResponseWriter, r *http.Request) (provider.SettingsCapable, string, bool) {
	p, ok := a.provider(w, r)
	if !ok {
		return nil, "", false
	}
	sc, ok := p.(provider.SettingsCapable)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台没有可在面板修改的参数")
		return nil, "", false
	}
	return sc, p.Meta().ID, true
}

func settingsOut(sc provider.SettingsCapable) providerSettingsOut {
	fields := sc.SettingsSchema()
	values, secrets := provider.SettingsValues(sc.SettingsRaw(), fields)
	out := providerSettingsOut{Fields: fields, Values: values, SecretsSet: secrets, PendingRestart: sc.PendingRestart()}
	if out.SecretsSet == nil {
		out.SecretsSet = []string{}
	}
	if out.PendingRestart == nil {
		out.PendingRestart = []string{}
	}
	return out
}

func (a *API) getProviderSettings(w http.ResponseWriter, r *http.Request) {
	sc, _, ok := a.settingsOf(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settingsOut(sc))
}

// putProviderSettings 只提交改动的字段：{"values": {"pool.max_in_flight": 4}}。
// 合并进 config.json 的 providers.<id> 段（保持文件精简：未改的字段不写入），
// 由平台校验并热应用，成功后落盘。
func (a *API) putProviderSettings(w http.ResponseWriter, r *http.Request) {
	sc, pid, ok := a.settingsOf(w, r)
	if !ok {
		return
	}
	if a.update == nil {
		writeErr(w, http.StatusNotImplemented, "配置不可写")
		return
	}
	var in struct {
		Values map[string]any `json:"values"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.Values) == 0 {
		httpx.WriteJSON(w, http.StatusOK, settingsOut(sc))
		return
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	merged, err := provider.MergeSettings(a.cfg().Providers[pid], sc.SettingsSchema(), in.Values)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := sc.ApplySettings(merged); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.update(func(c *config.Config) {
		if c.Providers == nil {
			c.Providers = map[string]json.RawMessage{}
		}
		c.Providers[pid] = merged
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "已生效，但写入配置文件失败: "+err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settingsOut(sc))
}
