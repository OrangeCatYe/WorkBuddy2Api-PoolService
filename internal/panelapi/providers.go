package panelapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/httpx"
)

// registerProviderRoutes 通用 Provider 能力接口：面板只依赖这些路由，
// 新平台实现对应能力接口即可获得登录、运维、任务等入口，无需改面板后端。
func (a *API) registerProviderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/providers/{pid}", a.auth(a.providerDetail))
	mux.HandleFunc("POST /api/providers/{pid}/login", a.auth(a.loginStart))
	mux.HandleFunc("GET /api/providers/{pid}/login/{sid}", a.auth(a.loginPoll))
	mux.HandleFunc("POST /api/providers/{pid}/accounts/{aid}/actions/{action}", a.auth(a.accountAction))
	mux.HandleFunc("POST /api/providers/{pid}/actions/{action}", a.auth(a.bulkAction))
	mux.HandleFunc("POST /api/providers/{pid}/accounts/{aid}/quota", a.auth(a.refreshQuota))
	mux.HandleFunc("POST /api/providers/{pid}/tasks/{tid}/run", a.auth(a.runTask))
}

func (a *API) provider(w http.ResponseWriter, r *http.Request) (provider.Provider, bool) {
	p, ok := a.reg.Get(r.PathValue("pid"))
	if !ok {
		writeErr(w, http.StatusNotFound, "未知平台 "+r.PathValue("pid"))
	}
	return p, ok
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	httpx.WriteJSON(w, status, map[string]any{"error": msg})
}

func decodeBody(r *http.Request, out any) error {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

// providerDetail 平台能力详情：登录方式、账号动作、任务。
func (a *API) providerDetail(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	out := map[string]any{"meta": provider.MetaWithCaps(p)}
	if l, ok := p.(provider.LoginCapable); ok {
		out["login_methods"] = l.LoginMethods()
	}
	if o, ok := p.(provider.AccountOps); ok {
		out["actions"] = o.Actions()
	}
	if t, ok := p.(provider.TaskCapable); ok {
		out["tasks"] = t.Tasks()
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) loginStart(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	l, ok := p.(provider.LoginCapable)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台不支持在面板添加账号")
		return
	}
	var body struct {
		Method string            `json:"method"`
		Params map[string]string `json:"params"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	sess, st, err := l.StartLogin(ctx, body.Method, body.Params)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	a.catalog.Invalidate()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"session": sess, "status": st})
}

func (a *API) loginPoll(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	l, ok := p.(provider.LoginCapable)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台不支持在面板添加账号")
		return
	}
	st, err := l.PollLogin(r.Context(), r.PathValue("sid"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if st != nil && st.State == "success" {
		a.catalog.Invalidate()
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (a *API) accountAction(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	o, ok := p.(provider.AccountOps)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台不支持账号操作")
		return
	}
	var params map[string]string
	_ = decodeBody(r, &params)
	res, err := o.Do(r.Context(), r.PathValue("aid"), r.PathValue("action"), params)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// bulkAction 批量动作：逐个执行，汇总结果（单个失败不中断）。
func (a *API) bulkAction(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	o, ok := p.(provider.AccountOps)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台不支持账号操作")
		return
	}
	var body struct {
		AccountIDs []string `json:"account_ids"`
	}
	if err := decodeBody(r, &body); err != nil || len(body.AccountIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "请选择账号")
		return
	}
	action := r.PathValue("action")
	type item struct {
		AccountID string `json:"account_id"`
		OK        bool   `json:"ok"`
		Message   string `json:"message,omitempty"`
	}
	results := make([]item, 0, len(body.AccountIDs))
	okCount := 0
	for _, id := range body.AccountIDs {
		res, err := o.Do(r.Context(), id, action, nil)
		switch {
		case err != nil:
			results = append(results, item{AccountID: id, Message: err.Error()})
		default:
			okCount++
			results = append(results, item{AccountID: id, OK: res.OK, Message: res.Message})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": okCount, "total": len(body.AccountIDs), "results": results})
}

func (a *API) refreshQuota(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	q, ok := p.(provider.QuotaCapable)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台不支持额度查询")
		return
	}
	quota, err := q.RefreshQuota(r.Context(), r.PathValue("aid"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, quota)
}

func (a *API) runTask(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	t, ok := p.(provider.TaskCapable)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台没有任务")
		return
	}
	var body struct {
		AccountIDs []string `json:"account_ids"`
	}
	_ = decodeBody(r, &body)
	if err := t.RunTask(r.Context(), r.PathValue("tid"), body.AccountIDs); err != nil {
		var status = http.StatusBadRequest
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		writeErr(w, status, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "任务已开始，结果见运行日志"})
}
