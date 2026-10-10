package panelapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/httpx"
	"github.com/orangeruan/poolservice/internal/store/keys"
	"github.com/orangeruan/poolservice/internal/store/requests"
)

// SetStores 注入 Key 存储与请求日志。
func (a *API) SetStores(k *keys.Store, r *requests.Recorder) { a.keys, a.reqs = k, r }

func (a *API) registerGatewayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/keys", a.auth(a.listKeys))
	mux.HandleFunc("POST /api/keys", a.auth(a.createKey))
	mux.HandleFunc("PUT /api/keys/{id}", a.auth(a.updateKey))
	mux.HandleFunc("DELETE /api/keys/{id}", a.auth(a.deleteKey))
	mux.HandleFunc("POST /api/keys/{id}/rotate", a.auth(a.rotateKey))
	mux.HandleFunc("POST /api/keys/{id}/reset-usage", a.auth(a.resetKeyUsage))
	mux.HandleFunc("GET /api/requests", a.auth(a.listRequests))
	mux.HandleFunc("GET /api/usage/stats", a.auth(a.usageStats))
	mux.HandleFunc("GET /api/connect", a.auth(a.connectInfo))
}

// keyView 面板展示形态。
type keyView struct {
	keys.Key
	Builtin bool `json:"builtin,omitempty"`
}

func (a *API) listKeys(w http.ResponseWriter, _ *http.Request) {
	out := []keyView{}
	if legacy := a.cfg().Gateway.LegacyAPIKey; legacy != "" {
		out = append(out, keyView{Key: keys.Key{ID: "legacy", Name: "配置文件密钥", Secret: legacy, Enabled: true,
			Note: "来自 config.json 的 gateway.legacy_api_key，不受限额约束，只能在配置文件中修改"}, Builtin: true})
	}
	if a.keys != nil {
		for _, k := range a.keys.List() {
			out = append(out, keyView{Key: k})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		writeErr(w, http.StatusNotImplemented, "Key 存储不可用")
		return
	}
	var sp keys.Spec
	if err := decodeBody(r, &sp); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	k, err := a.keys.Create(sp, "")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, keyView{Key: k})
}

func (a *API) updateKey(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		writeErr(w, http.StatusNotImplemented, "Key 存储不可用")
		return
	}
	var sp keys.Spec
	if err := decodeBody(r, &sp); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	k, err := a.keys.Update(r.PathValue("id"), sp)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, keyView{Key: k})
}

func (a *API) deleteKey(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		writeErr(w, http.StatusNotImplemented, "Key 存储不可用")
		return
	}
	if err := a.keys.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) rotateKey(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		writeErr(w, http.StatusNotImplemented, "Key 存储不可用")
		return
	}
	k, err := a.keys.Rotate(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, keyView{Key: k})
}

func (a *API) resetKeyUsage(w http.ResponseWriter, r *http.Request) {
	if a.keys == nil {
		writeErr(w, http.StatusNotImplemented, "Key 存储不可用")
		return
	}
	k, err := a.keys.ResetUsage(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, keyView{Key: k})
}

func (a *API) listRequests(w http.ResponseWriter, r *http.Request) {
	if a.reqs == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"requests": []any{}})
		return
	}
	q := r.URL.Query()
	f := requests.Filter{
		Provider: q.Get("provider"), Model: q.Get("model"), KeyID: q.Get("key"),
		AccountID: q.Get("account"), Protocol: q.Get("protocol"), Status: q.Get("status"), Q: q.Get("q"),
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	if b := q.Get("before"); b != "" {
		f.Before, _ = time.Parse(time.RFC3339Nano, b)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"requests": a.reqs.Query(f)})
}

func (a *API) usageStats(w http.ResponseWriter, r *http.Request) {
	if a.reqs == nil {
		httpx.WriteJSON(w, http.StatusOK, requests.Stats{})
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	httpx.WriteJSON(w, http.StatusOK, a.reqs.Stats(hours))
}

// connectInfo 接入向导：根据请求的 Host 推导对外地址。
func (a *API) connectInfo(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	base := scheme + "://" + host
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"base_url":   base,
		"openai":     base + "/v1",
		"anthropic":  base,
		"listen":     a.cfg().Listen,
		"has_legacy": a.cfg().Gateway.LegacyAPIKey != "",
	})
}
