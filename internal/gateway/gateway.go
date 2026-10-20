// Package gateway 统一 API 网关：OpenAI Chat Completions、Anthropic Messages、
// OpenAI Responses 三种入口共用一条管线（鉴权 → 限额 → 模型解析 → 推理档位映射 →
// Provider 转发 → 协议翻译 → 用量记录）。
package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
	"github.com/orangeruan/poolservice/internal/httpx"
	"github.com/orangeruan/poolservice/internal/store/keys"
	"github.com/orangeruan/poolservice/internal/store/requests"
)

// 协议标识（请求日志 protocol 字段）。
const (
	ProtoChat      = "chat"
	ProtoMessages  = "messages"
	ProtoResponses = "responses"
)

// LegacyKeyID 配置文件中 gateway.legacy_api_key 的身份 ID。
const LegacyKeyID = "legacy"

// Gateway 网关。
type Gateway struct {
	cfg     func() *config.Config
	reg     *provider.Registry
	catalog *router.Catalog
	picker  provider.Picker
	keys    *keys.Store
	reqs    *requests.Recorder
	version string

	observers []func(requests.Entry)
}

// New 创建网关。
func New(cfg func() *config.Config, reg *provider.Registry, catalog *router.Catalog, picker provider.Picker) *Gateway {
	return &Gateway{cfg: cfg, reg: reg, catalog: catalog, picker: picker}
}

// SetVersion 设置 /healthz 报告的程序版本。更新程序靠它确认新版本真的跑起来了。
func (g *Gateway) SetVersion(v string) { g.version = v }

// SetKeys 注入下游 Key 存储（nil = 只接受配置文件中的 legacy_api_key）。
func (g *Gateway) SetKeys(s *keys.Store) { g.keys = s }

// SetRequests 注入请求日志记录器（nil = 只写进程日志）。
func (g *Gateway) SetRequests(r *requests.Recorder) { g.reqs = r }

// OnRecord 注册请求完成回调（如积分账本）。须在 Register 之前调用。
func (g *Gateway) OnRecord(fn func(requests.Entry)) { g.observers = append(g.observers, fn) }

// Register 挂载路由。
func (g *Gateway) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", g.healthz)
	mux.HandleFunc("GET /v1/models", g.models)
	mux.HandleFunc("POST /v1/chat/completions", g.chat)
	mux.HandleFunc("POST /v1/messages", g.messages)
	mux.HandleFunc("POST /v1/messages/count_tokens", g.countTokens)
	mux.HandleFunc("POST /v1/responses", g.responses)
}

// errWriter 各协议的分发前错误格式。
type errWriter func(w http.ResponseWriter, status int, code, msg string)

func openAIErr(w http.ResponseWriter, status int, code, msg string) {
	httpx.WriteOpenAIError(w, status, code, msg, 0)
}

// identity 已鉴权的调用方。
type identity struct {
	id, name string
	key      *keys.Key // nil = legacy / 匿名（不受限）
}

// apiKey 依次读取 Authorization: Bearer、x-api-key（Anthropic）、api-key（Azure 风格）。
func apiKey(r *http.Request) string {
	if t := httpx.BearerToken(r); t != "" {
		return t
	}
	if t := strings.TrimSpace(r.Header.Get("X-Api-Key")); t != "" {
		return t
	}
	return strings.TrimSpace(r.Header.Get("Api-Key"))
}

func (g *Gateway) authenticate(w http.ResponseWriter, r *http.Request, ew errWriter) (*identity, bool) {
	cfg := g.cfg()
	tok := apiKey(r)
	legacy := cfg.Gateway.LegacyAPIKey
	if tok != "" && legacy != "" && httpx.SecretEqual(tok, legacy) {
		return &identity{id: LegacyKeyID, name: "配置文件密钥"}, true
	}
	if tok != "" && g.keys != nil {
		k, denied := g.keys.Authenticate(tok)
		if denied == nil {
			return &identity{id: k.ID, name: k.Name, key: &k}, true
		}
		if denied.Status != http.StatusUnauthorized || denied.Code != "invalid_api_key" {
			if denied.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(denied.RetryAfter.Seconds()+0.999)))
			}
			ew(w, denied.Status, denied.Code, denied.Msg)
			return nil, false
		}
	}
	if tok == "" && cfg.Gateway.AllowAnonymous {
		return &identity{id: "anonymous", name: "匿名"}, true
	}
	ew(w, http.StatusUnauthorized, "invalid_api_key", "invalid or missing API key")
	return nil, false
}

func (g *Gateway) healthz(w http.ResponseWriter, r *http.Request) {
	total, usable := 0, 0
	for _, p := range g.reg.List() {
		for _, a := range p.Accounts() {
			total++
			if a.Usable() {
				usable++
			}
		}
	}
	w.Header().Set("X-Service", "poolservice")
	status := http.StatusOK
	if usable == 0 {
		status = http.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, status, map[string]any{"service": "poolservice", "version": g.version, "healthy": usable, "total": total})
}

func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	id, ok := g.authenticate(w, r, openAIErr)
	if !ok {
		return
	}
	entries := g.catalog.Entries(r.Context())
	if id.key != nil && len(id.key.Models) > 0 {
		kept := entries[:0:0]
		for _, e := range entries {
			if id.key.AllowsModel(e.Model.ID, e.FullID()) {
				kept = append(kept, e)
			}
		}
		entries = kept
	}
	gc := g.cfg().Gateway
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   ModelEntries(entries, NamingOptions{Style: gc.ModelNaming, CreditSuffix: gc.CreditInModelName}),
	})
}

func readJSON(r *http.Request) (map[string]any, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return nil, errors.New("request body is not valid JSON")
	}
	return obj, nil
}

func (g *Gateway) chat(w http.ResponseWriter, r *http.Request) {
	id, ok := g.authenticate(w, r, openAIErr)
	if !ok {
		return
	}
	obj, err := readJSON(r)
	if err != nil {
		openAIErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	g.serve(w, r, id, obj, ProtoChat, openAIErr, nil)
}

func (g *Gateway) messages(w http.ResponseWriter, r *http.Request) {
	id, ok := g.authenticate(w, r, writeAnthropicError)
	if !ok {
		return
	}
	in, err := readJSON(r)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	obj, stream, err := anthropicToChat(in)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	model, _ := in["model"].(string)
	g.serve(w, r, id, obj, ProtoMessages, writeAnthropicError, func(cw http.ResponseWriter) chatSink {
		return newAnthropicSink(cw, model, stream)
	})
}

func (g *Gateway) countTokens(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.authenticate(w, r, writeAnthropicError); !ok {
		return
	}
	in, err := readJSON(r)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	n := estimateTokens([]any{in["system"], in["messages"], in["tools"]})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"input_tokens": n})
}

func (g *Gateway) responses(w http.ResponseWriter, r *http.Request) {
	id, ok := g.authenticate(w, r, openAIErr)
	if !ok {
		return
	}
	in, err := readJSON(r)
	if err != nil {
		openAIErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	obj, stream, err := responsesToChat(in)
	if err != nil {
		openAIErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	model, _ := in["model"].(string)
	g.serve(w, r, id, obj, ProtoResponses, openAIErr, func(cw http.ResponseWriter) chatSink {
		return newResponsesSink(cw, model, stream, in)
	})
}

// serve 公共管线。obj 为 OpenAI Chat 请求体；mkSink 非 nil 时把输出翻译为对应协议。
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request, id *identity, obj map[string]any, proto string, ew errWriter, mkSink func(http.ResponseWriter) chatSink) {
	start := time.Now()
	reqModel, _ := obj["model"].(string)
	stream, _ := obj["stream"].(bool)
	ent := requests.Entry{
		ID: newRequestID(), Time: start, Protocol: proto, KeyID: id.id, KeyName: id.name,
		RequestedModel: reqModel, Stream: stream, ClientIP: clientIP(r), UserAgent: truncate(r.UserAgent(), 160),
	}
	w.Header().Set("X-Request-Id", ent.ID)
	reject := func(status int, code, msg string, retry time.Duration) {
		if retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds()+0.999)))
		}
		ew(w, status, code, msg)
		ent.Status, ent.Error, ent.ErrorKind = status, msg, code
		g.record(ent, start)
	}

	if strings.TrimSpace(reqModel) == "" {
		reject(http.StatusBadRequest, "model_required", "field 'model' is required", 0)
		return
	}
	entry, err := g.catalog.Resolve(r.Context(), reqModel)
	if err != nil {
		var unk router.ErrUnknownModel
		if errors.As(err, &unk) {
			// 有平台目录拉取失败时，「找不到」可能只是暂时不可用，返回 503 让客户端稍后重试。
			if errs := g.catalog.Errors(); len(errs) > 0 {
				var parts []string
				for p, e := range errs {
					parts = append(parts, p+": "+e)
				}
				reject(http.StatusServiceUnavailable, "catalog_unavailable",
					fmt.Sprintf("model %q not found; model catalog partially unavailable (%s)", reqModel, strings.Join(parts, "; ")), 30*time.Second)
				return
			}
			reject(http.StatusNotFound, "model_not_found", err.Error(), 0)
			return
		}
		reject(http.StatusBadGateway, "catalog_error", err.Error(), 0)
		return
	}
	ent.Provider, ent.Model = entry.Provider, entry.Model.ID
	if id.key != nil && !id.key.AllowsModel(entry.Model.ID, entry.FullID()) {
		reject(http.StatusForbidden, "model_not_allowed", fmt.Sprintf("model %q is not allowed for this API key", reqModel), 0)
		return
	}
	if id.key != nil && g.keys != nil {
		if d := g.keys.Allow(*id.key); d != nil {
			reject(d.Status, d.Code, d.Msg, d.RetryAfter)
			return
		}
	}
	p, ok := g.reg.Get(entry.Provider)
	chat, isChat := p.(provider.ChatCapable)
	if !ok || !isChat {
		reject(http.StatusServiceUnavailable, "provider_unavailable", "provider "+entry.Provider+" is not available", 0)
		return
	}

	// 推理强度：顶层 reasoning_effort（ttswitch 规范）优先，兼容 reasoningEffort 与 reasoning.effort。
	requested := pickEffort(obj)
	applied, mapped := router.MapEffort(requested, entry.Model.ReasoningLevels)
	delete(obj, "reasoningEffort")
	if applied != "" {
		obj["reasoning_effort"] = applied
	} else {
		delete(obj, "reasoning_effort")
	}
	if mapped {
		w.Header().Set("X-Reasoning-Effort-Requested", requested)
	}
	if applied != "" {
		w.Header().Set("X-Reasoning-Effort-Applied", applied)
	}
	ent.Effort, ent.EffortAsked = applied, requested
	obj["model"] = entry.Model.ID
	body, _ := json.Marshal(obj)

	req := &provider.ChatRequest{
		Model: entry.Model.ID, RequestedModel: reqModel, Body: body, Stream: stream,
		ReasoningEffort: applied, RequestID: ent.ID, ClientIP: ent.ClientIP, UserAgent: r.UserAgent(),
		KeyID: id.id, Picker: g.picker, Header: r.Header,
	}

	var pw *protoWriter
	var target http.ResponseWriter = w
	if mkSink != nil {
		pw = newProtoWriter(w, mkSink(w))
		target = pw
	}
	rw := newRespRewriter(target, reqModel)
	outcome := chat.ServeChat(r.Context(), req, rw)
	rw.Finish()
	if pw != nil {
		pw.Finish()
	}
	if outcome.Status == 0 {
		outcome.Status = rw.status
	}
	if rw.hasUsage {
		// 网关从响应中解析到的 usage 最权威（含 Provider 未回传的缓存 / 推理字段）。
		outcome.Usage, outcome.HasUsage = rw.usage, true
	}
	if outcome.ErrorMsg == "" {
		outcome.ErrorMsg = rw.errMsg
	}
	if outcome.TTFB == 0 {
		outcome.TTFB = rw.ttfb
	}

	u := outcome.Usage
	ent.Status, ent.AccountID, ent.Attempts = outcome.Status, outcome.AccountID, outcome.Attempts
	ent.ErrorKind, ent.Error = outcome.ErrorKind, outcome.ErrorMsg
	ent.PromptTokens, ent.CompletionTokens, ent.CachedTokens, ent.ReasoningTokens = u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.ReasoningTokens
	ent.Credit, ent.HasCredit = u.Credit, u.HasCredit
	ent.TTFBMs = outcome.TTFB.Milliseconds()
	g.record(ent, start)
}

// record 写请求日志、累计 Key 用量并输出一行进程日志。
func (g *Gateway) record(e requests.Entry, start time.Time) {
	e.DurationMs = time.Since(start).Milliseconds()
	e.OK = e.Status >= 200 && e.Status < 300 && e.Error == ""
	if g.reqs != nil {
		g.reqs.Add(e)
	}
	for _, fn := range g.observers {
		fn(e)
	}
	if g.keys != nil && e.KeyID != LegacyKeyID && e.KeyID != "anonymous" {
		g.keys.Record(e.KeyID, e.OK, e.PromptTokens, e.CompletionTokens, e.CachedTokens, e.Credit)
	}
	credit := "-"
	if e.HasCredit {
		credit = strconv.FormatFloat(e.Credit, 'f', -1, 64)
	}
	acct := e.AccountID
	if len(acct) > 8 {
		acct = acct[:8]
	}
	line := fmt.Sprintf("[gateway] %s %s %s/%s acct=%s key=%s status=%d stream=%v effort=%s in=%d out=%d cached=%d credit=%s ttfb=%dms dur=%dms",
		e.ID, e.Protocol, orDash(e.Provider), orDash(e.Model), orDash(acct), e.KeyName, e.Status, e.Stream, orDash(e.Effort),
		e.PromptTokens, e.CompletionTokens, e.CachedTokens, credit, e.TTFBMs, e.DurationMs)
	if e.Error != "" {
		line += " err=" + truncate(e.Error, 160)
	}
	log.Print(line)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func pickEffort(obj map[string]any) string {
	if v, ok := obj["reasoning_effort"].(string); ok && v != "" {
		return v
	}
	if v, ok := obj["reasoningEffort"].(string); ok && v != "" {
		return v
	}
	if rs, ok := obj["reasoning"].(map[string]any); ok {
		if v, ok := rs["effort"].(string); ok {
			return v
		}
	}
	return ""
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

func newRequestID() string {
	return "req_" + config.RandomKey("")[:24]
}

func newShortID() string {
	return config.RandomKey("")[:24]
}
