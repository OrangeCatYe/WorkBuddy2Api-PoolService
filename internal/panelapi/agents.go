package panelapi

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/orangeruan/poolservice/internal/agentcfg"
	"github.com/orangeruan/poolservice/internal/core/router"
	"github.com/orangeruan/poolservice/internal/gateway"
	"github.com/orangeruan/poolservice/internal/httpx"
	"github.com/orangeruan/poolservice/internal/store/keys"
)

// 快速导入：把本网关的地址、密钥与模型写进其他 Agent 的配置文件（见 docs/proposals/2026-10-20-agent-import.md）。
//
// 写入类接口（apply / remove）会修改本机文件，保护比导出更严：
//   - admin_key 必须已设置：否则任何网页都能跨站 POST 过来，把 Agent 改去连别的地址；
//   - 只接受来自本机回环地址、且没有转发头的请求：面板部署在远程服务器时，写的是服务器的文件而不是用户电脑的；
//   - 浏览器发起的请求必须同源（Origin 与 Host 一致，且不是 cross-site）。
// 预览和渲染不写文件，不受后两条限制。

const defaultKeyName = "默认密钥（全部模型）"

// agentState 快速导入用到的运行期状态。
type agentState struct {
	mu     sync.Mutex
	mgr    *agentcfg.Manager
	mgrDir string
	env    *agentcfg.Env
}

// SetAgentEnv 注入探测 Agent 配置用的环境，测试用；默认取当前进程环境。
func (a *API) SetAgentEnv(e agentcfg.Env) {
	a.agent.mu.Lock()
	a.agent.env = &e
	a.agent.mu.Unlock()
}

func (a *API) agentEnv() agentcfg.Env {
	a.agent.mu.Lock()
	defer a.agent.mu.Unlock()
	if a.agent.env != nil {
		return *a.agent.env
	}
	return agentcfg.DefaultEnv()
}

func (a *API) agentManager() *agentcfg.Manager {
	a.agent.mu.Lock()
	defer a.agent.mu.Unlock()
	dir := a.cfg().DataDir
	if a.agent.mgr == nil || a.agent.mgrDir != dir {
		a.agent.mgr, a.agent.mgrDir = agentcfg.NewManager(dir), dir
	}
	return a.agent.mgr
}

func (a *API) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents", a.auth(a.listAgents))
	mux.HandleFunc("POST /api/agents/{id}/detect", a.auth(a.agentDetect))
	mux.HandleFunc("POST /api/agents/{id}/preview", a.auth(a.agentPreview))
	mux.HandleFunc("POST /api/agents/{id}/render", a.auth(a.agentRender))
	mux.HandleFunc("POST /api/agents/{id}/apply", a.auth(a.agentApply))
	mux.HandleFunc("POST /api/agents/{id}/remove", a.auth(a.agentRemove))
}

// ---- 请求保护 ----

func isLocalRequest(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "Forwarded"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true // curl 等非浏览器客户端不带 Origin
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// writeBlock 返回不允许写入本机文件的原因；空串表示允许。
func (a *API) writeBlock(r *http.Request) string {
	switch {
	case a.cfg().AdminKey == "":
		return "写入本机配置需要先在 config.json 中设置 admin_key，否则任何网页都能借用这个接口修改你的配置"
	case !isLocalRequest(r):
		return "只能在运行本服务的这台电脑上写入。当前请求不是来自本机，请改用「复制配置」手动粘贴"
	case !sameOrigin(r):
		return "拒绝跨站请求"
	}
	return ""
}

// ---- 请求体与解析 ----

type agentReq struct {
	KeyID        string   `json:"key_id"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
	FastModel    string   `json:"fast_model"`
	OpusModel    string   `json:"opus_model"`
	SonnetModel  string   `json:"sonnet_model"`
	Profile      string   `json:"profile"`
	ConfigDir    string   `json:"config_dir"`
	BaseURL      string   `json:"base_url"`
}

type agentModel struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Provider  string   `json:"provider"`
	Context   int      `json:"context,omitempty"`
	MaxOutput int      `json:"max_output,omitempty"`
	Vision    bool     `json:"vision,omitempty"`
	Tools     bool     `json:"tools,omitempty"`
	Reasoning bool     `json:"reasoning,omitempty"`
	Efforts   []string `json:"efforts,omitempty"`
	// DefaultEffort 模型目录声明的默认思考档。
	DefaultEffort string `json:"default_effort,omitempty"`
}

func (m agentModel) model() agentcfg.Model {
	return agentcfg.Model{ID: m.ID, Name: m.Name, Context: m.Context, MaxOutput: m.MaxOutput,
		Vision: m.Vision, Tools: m.Tools, Reasoning: m.Reasoning, Efforts: m.Efforts, DefaultEffort: m.DefaultEffort}
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func asStrs(v any) []string {
	s, _ := v.([]string)
	return s
}

// catalogModels 按网关 /v1/models 的命名口径列出模型，写进 Agent 的 ID 与网关实际接受的一致。
func (a *API) catalogModels(r *http.Request) []agentModel {
	gc := a.cfg().Gateway
	entries := a.catalog.Entries(r.Context())
	var out []agentModel
	for _, e := range gateway.ModelEntries(entries, gateway.NamingOptions{Style: gc.ModelNaming, CreditSuffix: gc.CreditInModelName}) {
		id, _ := e["id"].(string)
		if id == "" {
			continue
		}
		name, _ := e["display_name"].(string)
		prov, _ := e["provider"].(string)
		sup, _ := e["supports_tools"].(bool)
		rea, _ := e["supports_reasoning"].(bool)
		defEffort, _ := e["reasoning_default_effort"].(string)
		vision := false
		for _, mod := range asStrs(e["input_modalities"]) {
			if mod == "image" {
				vision = true
			}
		}
		out = append(out, agentModel{
			ID: id, Name: name, Provider: prov,
			Context: asInt(e["context_window"]), MaxOutput: asInt(e["max_output_tokens"]),
			Vision: vision, Tools: sup, Reasoning: rea, Efforts: asStrs(e["reasoning_levels"]), DefaultEffort: defEffort,
		})
	}
	return out
}

func keyAllows(k keys.Key, provider, id string) bool {
	base := router.StripCreditSuffix(id)
	names := []string{base, provider + "/" + base}
	if p := provider + "/"; strings.HasPrefix(base, p) {
		names = append(names, strings.TrimPrefix(base, p))
	}
	return k.AllowsModel(names...)
}

type keyChoice struct {
	ID, Name, Secret string
	Create           bool
	Key              *keys.Key
}

// resolveKey 选定要写进 Agent 的密钥。由用户指定；只有一把密钥都没有时才建议创建默认密钥。
func (a *API) resolveKey(keyID string) (keyChoice, error) {
	legacy := a.cfg().Gateway.LegacyAPIKey
	var list []keys.Key
	if a.keys != nil {
		list = a.keys.List()
	}
	if keyID == "legacy" {
		if legacy == "" {
			return keyChoice{}, errors.New("没有配置文件密钥")
		}
		return keyChoice{ID: "legacy", Name: "配置文件密钥", Secret: legacy}, nil
	}
	if keyID != "" {
		for i := range list {
			if list[i].ID != keyID {
				continue
			}
			k := list[i]
			if !k.Enabled || (k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now())) {
				return keyChoice{}, errors.New("所选密钥已停用或已过期")
			}
			return keyChoice{ID: k.ID, Name: k.Name, Secret: k.Secret, Key: &k}, nil
		}
		return keyChoice{}, errors.New("所选密钥不存在")
	}
	if len(list) == 0 && legacy == "" && a.keys != nil {
		return keyChoice{Name: defaultKeyName, Secret: keys.NewSecret(), Create: true}, nil
	}
	return keyChoice{}, errors.New("请选择要写入的密钥")
}

// requestBase 由请求推导网关对外地址。本机请求把 localhost 换成 127.0.0.1，
// 避免 Node 系 Agent 把 localhost 解析成 ::1 而服务只监听 IPv4。
func requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	if isLocalRequest(r) {
		if h, port, err := net.SplitHostPort(host); err == nil && strings.EqualFold(h, "localhost") {
			host = net.JoinHostPort("127.0.0.1", port)
		} else if strings.EqualFold(host, "localhost") {
			host = "127.0.0.1"
		}
	}
	return scheme + "://" + host
}

func resolveBase(r *http.Request, override string) (string, error) {
	override = strings.TrimSpace(override)
	if override == "" {
		return requestBase(r), nil
	}
	u, err := url.Parse(override)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("网关地址必须是 http:// 或 https:// 开头的完整地址")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

// ---- 计划 / 结果 ----

type planFile struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // create / update / delete / unchanged
	Snippet string `json:"snippet,omitempty"`
	Lang    string `json:"lang,omitempty"`
}

type agentPlan struct {
	Agent     string          `json:"agent"`
	Files     []planFile      `json:"files"`
	Imported  []string        `json:"imported,omitempty"`
	Skipped   []agentcfg.Skip `json:"skipped,omitempty"`
	Warnings  []string        `json:"warnings,omitempty"`
	Key       *planKey        `json:"key,omitempty"`
	Restart   string          `json:"restart,omitempty"`
	BackupDir string          `json:"backup_dir,omitempty"`
	Written   []string        `json:"written,omitempty"`
	BaseURL   string          `json:"base_url,omitempty"`
}

type planKey struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Created bool   `json:"created,omitempty"`
	WillNew bool   `json:"will_create,omitempty"`
}

func buildPlan(adapter agentcfg.Adapter, res *agentcfg.Result, mask bool) agentPlan {
	p := agentPlan{Agent: adapter.ID(), Imported: res.Imported, Skipped: res.Skipped, Warnings: res.Warnings}
	for _, c := range res.Changes {
		f := planFile{Path: c.Path, Lang: c.Lang, Snippet: c.Snippet}
		if mask {
			for _, s := range c.Secrets {
				if s != "" {
					f.Snippet = strings.ReplaceAll(f.Snippet, s, agentcfg.MaskSecret(s))
				}
			}
		}
		switch {
		case c.Delete:
			f.Action = "delete"
		case !c.Existed:
			f.Action = "create"
		case string(c.Before) == string(c.After):
			f.Action = "unchanged"
		default:
			f.Action = "update"
		}
		p.Files = append(p.Files, f)
	}
	return p
}

// prepared 一次请求解析后的中间结果。
type prepared struct {
	adapter agentcfg.Adapter
	in      agentcfg.Input
	key     keyChoice
	skipped []agentcfg.Skip
	base    string
}

func (a *API) adapterOf(w http.ResponseWriter, r *http.Request) (agentcfg.Adapter, bool) {
	ad := agentcfg.Find(r.PathValue("id"))
	if ad == nil {
		writeErr(w, http.StatusNotFound, "未知的 Agent")
		return nil, false
	}
	return ad, true
}

func (a *API) prepare(w http.ResponseWriter, r *http.Request, ad agentcfg.Adapter) (*prepared, bool) {
	var req agentReq
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	base, err := resolveBase(r, req.BaseURL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	key, err := a.resolveKey(req.KeyID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	byID := map[string]agentModel{}
	for _, m := range a.catalogModels(r) {
		if _, dup := byID[m.ID]; !dup {
			byID[m.ID] = m
		}
	}
	pr := &prepared{adapter: ad, key: key, base: base}
	seen := map[string]bool{}
	var models []agentcfg.Model
	for _, id := range req.Models {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		m, ok := byID[id]
		switch {
		case !ok:
			pr.skipped = append(pr.skipped, agentcfg.Skip{ID: id, Reason: "模型目录中没有这个模型"})
		case key.Key != nil && !keyAllows(*key.Key, m.Provider, m.ID):
			pr.skipped = append(pr.skipped, agentcfg.Skip{ID: id, Reason: "所选密钥没有使用这个模型的权限"})
		default:
			models = append(models, m.model())
		}
	}
	has := func(id string) bool {
		for _, m := range models {
			if m.ID == id {
				return true
			}
		}
		return false
	}
	def := strings.TrimSpace(req.DefaultModel)
	fast := strings.TrimSpace(req.FastModel)
	opus := strings.TrimSpace(req.OpusModel)
	sonnet := strings.TrimSpace(req.SonnetModel)
	if def != "" && !has(def) {
		writeErr(w, http.StatusBadRequest, "默认模型必须是已选择且所选密钥可用的模型之一")
		return nil, false
	}
	for _, c := range []struct{ id, label string }{{fast, "Haiku 档（快速）模型"}, {opus, "Opus 档模型"}, {sonnet, "Sonnet 档模型"}} {
		if c.id != "" && !has(c.id) {
			writeErr(w, http.StatusBadRequest, c.label+"必须是已选择且所选密钥可用的模型之一")
			return nil, false
		}
	}
	pr.in = agentcfg.Input{
		BaseURL: base, APIKey: key.Secret, Models: models,
		Default: def, Fast: fast, Opus: opus, Sonnet: sonnet, Profile: strings.TrimSpace(req.Profile), Dir: strings.TrimSpace(req.ConfigDir),
	}
	return pr, true
}

func (a *API) build(w http.ResponseWriter, pr *prepared) (*agentcfg.Result, bool) {
	res, err := pr.adapter.Build(a.agentEnv(), pr.in, a.agentManager().LoadPrev(pr.adapter.ID()))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	res.Skipped = append(append([]agentcfg.Skip(nil), pr.skipped...), res.Skipped...)
	return res, true
}

func (pr *prepared) planKey() *planKey {
	return &planKey{ID: pr.key.ID, Name: pr.key.Name, WillNew: pr.key.Create}
}

// ---- 处理器 ----

type detectReq struct {
	Profile   string `json:"profile"`
	ConfigDir string `json:"config_dir"`
}

func (a *API) listAgents(w http.ResponseWriter, r *http.Request) {
	env, mgr := a.agentEnv(), a.agentManager()
	var infos []agentcfg.Info
	for _, ad := range agentcfg.All() {
		infos = append(infos, ad.Detect(env, agentcfg.Options{}, mgr.LoadPrev(ad.ID())))
	}
	models := a.catalogModels(r)
	if kid := r.URL.Query().Get("key_id"); kid != "" {
		if kc, err := a.resolveKey(kid); err == nil && kc.Key != nil {
			filtered := models[:0:0]
			for _, m := range models {
				if keyAllows(*kc.Key, m.Provider, m.ID) {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		}
	}
	block := a.writeBlock(r)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"agents":      infos,
		"models":      models,
		"base_url":    requestBase(r),
		"can_write":   block == "",
		"write_block": block,
		"local":       isLocalRequest(r),
		"no_keys":     a.keys != nil && len(a.keys.List()) == 0 && a.cfg().Gateway.LegacyAPIKey == "",
	})
}

func (a *API) agentDetect(w http.ResponseWriter, r *http.Request) {
	ad, ok := a.adapterOf(w, r)
	if !ok {
		return
	}
	var req detectReq
	_ = decodeBody(r, &req)
	info := ad.Detect(a.agentEnv(), agentcfg.Options{Profile: strings.TrimSpace(req.Profile), Dir: strings.TrimSpace(req.ConfigDir)}, a.agentManager().LoadPrev(ad.ID()))
	httpx.WriteJSON(w, http.StatusOK, info)
}

func (a *API) agentPreview(w http.ResponseWriter, r *http.Request) {
	ad, ok := a.adapterOf(w, r)
	if !ok {
		return
	}
	pr, ok := a.prepare(w, r, ad)
	if !ok {
		return
	}
	res, ok := a.build(w, pr)
	if !ok {
		return
	}
	plan := buildPlan(ad, res, true)
	plan.Key, plan.BaseURL = pr.planKey(), pr.base
	httpx.WriteJSON(w, http.StatusOK, plan)
}

// agentRender 返回未遮掩的受管片段，用于面板部署在远程时手动复制。
func (a *API) agentRender(w http.ResponseWriter, r *http.Request) {
	ad, ok := a.adapterOf(w, r)
	if !ok {
		return
	}
	pr, ok := a.prepare(w, r, ad)
	if !ok {
		return
	}
	if pr.key.Create {
		writeErr(w, http.StatusBadRequest, "还没有任何密钥，请先在「接入与密钥」里创建一把，或在本机直接写入以自动创建默认密钥")
		return
	}
	res, ok := a.build(w, pr)
	if !ok {
		return
	}
	plan := buildPlan(ad, res, false)
	plan.Key, plan.BaseURL = pr.planKey(), pr.base
	httpx.WriteJSON(w, http.StatusOK, plan)
}

func (a *API) agentApply(w http.ResponseWriter, r *http.Request) {
	ad, ok := a.adapterOf(w, r)
	if !ok {
		return
	}
	if msg := a.writeBlock(r); msg != "" {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	pr, ok := a.prepare(w, r, ad)
	if !ok {
		return
	}
	res, ok := a.build(w, pr)
	if !ok {
		return
	}
	pk := pr.planKey()
	// 先算后建：确认配置能生成之后才创建默认密钥，避免失败时留下无用的密钥。
	if pr.key.Create {
		k, err := a.keys.Create(keys.Spec{Name: defaultKeyName, Note: "由「快速导入」自动创建：没有任何密钥时使用，不限模型、不限额"}, pr.key.Secret)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "创建默认密钥失败："+err.Error())
			return
		}
		pk = &planKey{ID: k.ID, Name: k.Name, Created: true}
	}
	applied, err := a.agentManager().Apply(ad.ID(), res)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, agentcfg.ErrChanged) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	log.Printf("[panel] 快速导入 %s：%d 个模型，写入 %d 个文件", ad.ID(), len(res.Imported), len(applied.Written))
	plan := buildPlan(ad, res, true)
	plan.Key, plan.BaseURL = pk, pr.base
	plan.BackupDir, plan.Written = applied.BackupDir, applied.Written
	plan.Restart = ad.Detect(a.agentEnv(), agentcfg.Options{Profile: pr.in.Profile, Dir: pr.in.Dir}, nil).Restart
	httpx.WriteJSON(w, http.StatusOK, plan)
}

func (a *API) agentRemove(w http.ResponseWriter, r *http.Request) {
	ad, ok := a.adapterOf(w, r)
	if !ok {
		return
	}
	if msg := a.writeBlock(r); msg != "" {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	var req detectReq
	_ = decodeBody(r, &req)
	opt := agentcfg.Options{Profile: strings.TrimSpace(req.Profile), Dir: strings.TrimSpace(req.ConfigDir)}
	mgr := a.agentManager()
	res, err := ad.Remove(a.agentEnv(), opt, mgr.LoadPrev(ad.ID()))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	applied, err := mgr.Apply(ad.ID(), res)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, agentcfg.ErrChanged) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	log.Printf("[panel] 快速导入 %s：已移除，改动 %d 个文件", ad.ID(), len(applied.Written))
	plan := buildPlan(ad, res, true)
	plan.BackupDir, plan.Written = applied.BackupDir, applied.Written
	plan.Restart = ad.Detect(a.agentEnv(), opt, nil).Restart
	httpx.WriteJSON(w, http.StatusOK, plan)
}
