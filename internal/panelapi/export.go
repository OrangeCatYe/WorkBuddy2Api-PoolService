package panelapi

import (
	"log"
	"net/http"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/httpx"
)

// exportAccounts 导出所选账号的配置，供「添加账号 → 导入凭证」回环导入。
//
// 响应含登录凭证明文，是整个面板里最敏感的出口：
//   - 只走 POST，令牌不会出现在 URL / 访问日志里；a.auth 已统一加 Cache-Control: no-store
//   - admin_key 为空意味着面板对外无鉴权，此时拒绝放出凭证
//   - 日志只记平台与数量，不记账号 ID 以外的任何内容
func (a *API) exportAccounts(w http.ResponseWriter, r *http.Request) {
	p, ok := a.provider(w, r)
	if !ok {
		return
	}
	e, ok := p.(provider.AccountExporter)
	if !ok {
		writeErr(w, http.StatusBadRequest, "该平台不支持导出账号配置")
		return
	}
	if a.cfg().AdminKey == "" {
		writeErr(w, http.StatusForbidden, "导出内容含登录凭证，请先在 config.json 中设置 admin_key")
		return
	}
	var body struct {
		AccountIDs []string `json:"account_ids"`
	}
	if err := decodeBody(r, &body); err != nil || len(body.AccountIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "请选择要导出的账号")
		return
	}
	res, err := e.ExportAccounts(r.Context(), body.AccountIDs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[panel] 导出 %s 账号配置 %d 个", p.Meta().ID, res.Count)
	httpx.WriteJSON(w, http.StatusOK, res)
}
