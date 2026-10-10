// Package gateway OpenAI 兼容网关入口。
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
)

// Gateway 网关。
type Gateway struct {
	cfg     func() *config.Config
	reg     *provider.Registry
	catalog *router.Catalog
	picker  provider.Picker
}

// New 创建网关。
func New(cfg func() *config.Config, reg *provider.Registry, catalog *router.Catalog, picker provider.Picker) *Gateway {
	return &Gateway{cfg: cfg, reg: reg, catalog: catalog, picker: picker}
}

// Register 挂载路由。
func (g *Gateway) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", g.healthz)
	mux.HandleFunc("GET /v1/models", g.auth(g.models))
	mux.HandleFunc("POST /v1/chat/completions", g.auth(g.chat))
}

func (g *Gateway) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := g.cfg()
		key := cfg.Gateway.LegacyAPIKey
		if key == "" && cfg.Gateway.AllowAnonymous {
			next(w, r)
			return
		}
		tok := httpx.BearerToken(r)
		if tok == "" || key == "" || !httpx.SecretEqual(tok, key) {
			httpx.WriteOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "invalid or missing API key", 0)
			return
		}
		next(w, r)
	}
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
	httpx.WriteJSON(w, status, map[string]any{"service": "poolservice", "healthy": usable, "total": total})
}

func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	entries := g.catalog.Entries(r.Context())
	gc := g.cfg().Gateway
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   ModelEntries(entries, NamingOptions{Style: gc.ModelNaming, CreditSuffix: gc.CreditInModelName}),
	})
}

func (g *Gateway) chat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteOpenAIError(w, http.StatusBadRequest, "invalid_request", "read body: "+err.Error(), 0)
		return
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj == nil {
		httpx.WriteOpenAIError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON", 0)
		return
	}
	reqModel, _ := obj["model"].(string)
	if strings.TrimSpace(reqModel) == "" {
		httpx.WriteOpenAIError(w, http.StatusBadRequest, "model_required", "field 'model' is required", 0)
		return
	}
	entry, err := g.catalog.Resolve(r.Context(), reqModel)
	if err != nil {
		var unk router.ErrUnknownModel
		if errors.As(err, &unk) {
			httpx.WriteOpenAIError(w, http.StatusNotFound, "model_not_found", err.Error(), 0)
			return
		}
		httpx.WriteOpenAIError(w, http.StatusBadGateway, "catalog_error", err.Error(), 0)
		return
	}
	p, ok := g.reg.Get(entry.Provider)
	chat, isChat := p.(provider.ChatCapable)
	if !ok || !isChat {
		httpx.WriteOpenAIError(w, http.StatusServiceUnavailable, "provider_unavailable", "provider "+entry.Provider+" is not available", 0)
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
	obj["model"] = entry.Model.ID
	stream, _ := obj["stream"].(bool)
	out, _ := json.Marshal(obj)

	req := &provider.ChatRequest{
		Model:           entry.Model.ID,
		RequestedModel:  reqModel,
		Body:            out,
		Stream:          stream,
		ReasoningEffort: applied,
		RequestID:       newRequestID(),
		ClientIP:        clientIP(r),
		UserAgent:       r.UserAgent(),
		Picker:          g.picker,
		Header:          r.Header,
	}
	w.Header().Set("X-Request-Id", req.RequestID)
	rw := newRespRewriter(w, reqModel)
	outcome := chat.ServeChat(r.Context(), req, rw)
	rw.Finish()
	if outcome.Status == 0 {
		outcome.Status = rw.status
	}
	if !outcome.HasUsage && rw.hasUsage {
		outcome.Usage, outcome.HasUsage = rw.usage, true
	}
	if outcome.ErrorMsg == "" {
		outcome.ErrorMsg = rw.errMsg
	}
	if outcome.TTFB == 0 {
		outcome.TTFB = rw.ttfb
	}
	logChat(req, entry.Provider, outcome, time.Since(rw.started))
}

// logChat 输出一行请求摘要（P5 起接入请求日志存储）。
func logChat(req *provider.ChatRequest, prov string, o provider.ChatOutcome, dur time.Duration) {
	u := o.Usage
	credit := "-"
	if u.HasCredit {
		credit = strconv.FormatFloat(u.Credit, 'f', -1, 64)
	}
	line := fmt.Sprintf("[gateway] %s %s/%s status=%d stream=%v effort=%s in=%d out=%d cached=%d credit=%s ttfb=%s dur=%s",
		req.RequestID, prov, req.Model, o.Status, req.Stream, orDash(req.ReasoningEffort),
		u.PromptTokens, u.CompletionTokens, u.CachedTokens, credit,
		o.TTFB.Round(time.Millisecond), dur.Round(time.Millisecond))
	if o.ErrorMsg != "" {
		line += " err=" + truncate(o.ErrorMsg, 160)
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
