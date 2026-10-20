package panelapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/httpx"
	"github.com/orangeruan/poolservice/internal/update"
)

// SetUpdater 注入软件更新服务。
func (a *API) SetUpdater(s *update.Service) { a.upd = s }

func (a *API) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update/status", a.auth(a.updateStatus))
	mux.HandleFunc("POST /api/update/check", a.auth(a.updateCheck))
	mux.HandleFunc("POST /api/update/download", a.auth(a.updateDownload))
	mux.HandleFunc("POST /api/update/apply", a.auth(a.updateApply))
	mux.HandleFunc("GET /api/settings/update", a.auth(a.getUpdateSettings))
	mux.HandleFunc("PUT /api/settings/update", a.auth(a.putUpdateSettings))
}

// updaterOrErr 未装配更新服务时返回 501。
func (a *API) updaterOrErr(w http.ResponseWriter) (*update.Service, bool) {
	if a.upd == nil {
		writeErr(w, http.StatusNotImplemented, "软件更新不可用")
		return nil, false
	}
	return a.upd, true
}

// requireAdminKey 会改变系统状态的更新操作要求设置了 admin_key：
// 空密钥意味着面板对外无鉴权，此时任何能访问端口的人都可以触发下载并替换程序。
func (a *API) requireAdminKey(w http.ResponseWriter) bool {
	if a.cfg().AdminKey == "" {
		writeErr(w, http.StatusForbidden, "需要先在 config.json 中设置 admin_key 才能使用此功能")
		return false
	}
	return true
}

// writeUpdateErr 把服务层错误映射为 HTTP 状态：正忙 409，前置条件不满足 400，其余 500。
func writeUpdateErr(w http.ResponseWriter, err error) {
	var rj *update.RejectError
	switch {
	case errors.Is(err, update.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.As(err, &rj):
		writeErr(w, http.StatusBadRequest, rj.Msg)
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func (a *API) updateStatus(w http.ResponseWriter, _ *http.Request) {
	if u, ok := a.updaterOrErr(w); ok {
		httpx.WriteJSON(w, http.StatusOK, u.Status())
	}
}

// updateCheck 检查更新。请求体只能给下载源 ID，地址只来自配置，接口不能被用来访问任意 URL。
func (a *API) updateCheck(w http.ResponseWriter, r *http.Request) {
	u, ok := a.updaterOrErr(w)
	if !ok {
		return
	}
	var body struct {
		Source string `json:"source"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := u.Check(ctx, strings.TrimSpace(body.Source)); err != nil {
		writeUpdateErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u.Status())
}

func (a *API) updateDownload(w http.ResponseWriter, _ *http.Request) {
	u, ok := a.updaterOrErr(w)
	if !ok || !a.requireAdminKey(w) {
		return
	}
	if err := u.Download(); err != nil {
		writeUpdateErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, u.Status())
}

func (a *API) updateApply(w http.ResponseWriter, _ *http.Request) {
	u, ok := a.updaterOrErr(w)
	if !ok || !a.requireAdminKey(w) {
		return
	}
	if err := u.Apply(); err != nil {
		writeUpdateErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, u.Status())
}

// ---- 更新设置 ----

type updateSettings struct {
	Source        string          `json:"source"`
	Sources       []update.Source `json:"sources"` // 内置源在前（builtin=true），其余为自定义源
	Proxy         string          `json:"proxy"`   // 密码已隐藏
	AutoCheck     bool            `json:"auto_check"`
	CheckInterval string          `json:"check_interval"`
}

func updateSettingsOf(c config.UpdateConfig) updateSettings {
	return updateSettings{
		Source: c.Source, Sources: update.Sources(c), Proxy: update.MaskProxy(c.Proxy),
		AutoCheck: c.AutoCheck, CheckInterval: c.CheckInterval.Duration.String(),
	}
}

func (a *API) getUpdateSettings(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, updateSettingsOf(a.cfg().Update))
}

func (a *API) putUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if a.update == nil {
		writeErr(w, http.StatusNotImplemented, "配置不可写")
		return
	}
	if !a.requireAdminKey(w) {
		return
	}
	var in struct {
		Source        string          `json:"source"`
		Sources       []update.Source `json:"sources"`
		Proxy         string          `json:"proxy"`
		AutoCheck     bool            `json:"auto_check"`
		CheckInterval string          `json:"check_interval"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误: "+err.Error())
		return
	}

	// 只接受自定义源；内置源由程序决定，忽略请求里的同名条目。
	var custom []config.UpdateSource
	for _, s := range in.Sources {
		if s.Builtin || s.ID == update.BuiltinID {
			continue
		}
		custom = append(custom, config.UpdateSource{ID: strings.TrimSpace(s.ID), Name: strings.TrimSpace(s.Name), ManifestURL: strings.TrimSpace(s.ManifestURL), AssetURL: strings.TrimSpace(s.AssetURL)})
	}
	if err := update.ValidateSources(custom); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	cur := a.cfg().Update
	// 前端拿到的代理地址已隐藏密码；原样提交说明用户没改，保留原值，不能把 *** 存成密码。
	proxy := strings.TrimSpace(in.Proxy)
	if proxy != "" && proxy == update.MaskProxy(cur.Proxy) {
		proxy = cur.Proxy
	}
	if proxy != "" {
		if _, err := update.ParseProxy(proxy); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	next := config.UpdateConfig{Source: strings.TrimSpace(in.Source), Sources: custom, Proxy: proxy, AutoCheck: in.AutoCheck, CheckInterval: cur.CheckInterval}
	if iv := strings.TrimSpace(in.CheckInterval); iv != "" {
		d, err := time.ParseDuration(iv)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "check_interval 格式无效，示例：6h、24h")
			return
		}
		if d < config.MinCheckInterval {
			writeErr(w, http.StatusBadRequest, "自动检查间隔最短 1 小时")
			return
		}
		next.CheckInterval = config.Duration{Duration: d}
	}
	next.Normalize()
	if _, err := update.Find(next, next.Source); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.update(func(c *config.Config) { c.Update = next }); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, updateSettingsOf(a.cfg().Update))
}
