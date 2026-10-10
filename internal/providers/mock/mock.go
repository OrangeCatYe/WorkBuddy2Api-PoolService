// Package mock 开发联调用 Provider：内存账号 + 回显式聊天，不访问任何上游。
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
)

// Provider mock 实现。
type Provider struct {
	mu       sync.Mutex
	accounts []account.Account
}

// New 创建带 3 个示例账号的 mock Provider。
func New() *Provider {
	now := time.Now()
	in := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	mk := func(id, label string, remain, total float64, pk []account.Package) account.Account {
		return account.Account{
			ID: id, Provider: "mock", Label: label, Status: account.StatusActive,
			Quota: &account.Quota{Unit: "credit", Remaining: remain, Total: total, Packages: pk, UpdatedAt: now},
		}
	}
	return &Provider{accounts: []account.Account{
		mk("m1", "演示账号 A", 1800, 3000, []account.Package{
			{Name: "签到奖励", Source: "checkin", Amount: 500, Remaining: 300, ExpireAt: in(36 * time.Hour)},
			{Name: "新手礼包", Source: "gift", Amount: 2500, Remaining: 1500, ExpireAt: in(20 * 24 * time.Hour)},
		}),
		mk("m2", "演示账号 B", 420, 1000, []account.Package{
			{Name: "任务奖励", Source: "task", Amount: 1000, Remaining: 420, ExpireAt: in(5 * 24 * time.Hour)},
		}),
		mk("m3", "演示账号 C", 0, 500, nil),
	}}
}

// Meta 元信息。
func (p *Provider) Meta() provider.Meta {
	return provider.Meta{
		ID: "mock", Name: "Mock", Description: "开发联调用的模拟平台，不访问任何上游",
		Icon: "flask",
	}
}

// Accounts 账号快照。
func (p *Provider) Accounts() []account.Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]account.Account, len(p.accounts))
	copy(out, p.accounts)
	for i := range out {
		if out[i].Quota != nil && out[i].Quota.Remaining <= 0 {
			out[i].Status = account.StatusCooling
			out[i].StatusReason = "积分耗尽"
		}
	}
	return out
}

// Models 模型目录。
func (p *Provider) Models(context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{ID: "mock-echo", DisplayName: "Mock Echo", ContextWindow: 128000, MaxOutputTokens: 8192,
			SupportsTools: true, CreditRate: 0, CreditRateText: "x0"},
		{ID: "mock-think", DisplayName: "Mock Thinking", ContextWindow: 200000, MaxOutputTokens: 32000,
			SupportsTools: true, SupportsImages: true, SupportsThinking: true,
			ReasoningLevels: []string{"low", "medium", "high"}, DefaultReasoning: "medium",
			CreditRate: 0.5, CreditRateText: "x0.5"},
	}, nil
}

// ServeChat 回显最后一条 user 消息。
func (p *Provider) ServeChat(ctx context.Context, req *provider.ChatRequest, w http.ResponseWriter) provider.ChatOutcome {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(req.Body, &body)
	last := ""
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role == "user" {
			if s, ok := body.Messages[i].Content.(string); ok {
				last = s
			}
			break
		}
	}
	text := fmt.Sprintf("[mock:%s effort=%s] %s", req.Model, req.ReasoningEffort, last)
	usage := provider.Usage{PromptTokens: int64(len(last)/4 + 1), CompletionTokens: int64(len(text)/4 + 1)}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	id := "chatcmpl-" + req.RequestID
	created := time.Now().Unix()
	usageObj := map[string]any{
		"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens,
		"total_tokens": usage.TotalTokens,
	}

	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": text}}},
			"usage": usageObj,
		})
		return provider.ChatOutcome{AccountID: "m1", Attempts: 1, Status: 200, Usage: usage, HasUsage: true}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, _ := w.(http.Flusher)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	send(chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	for _, word := range strings.SplitAfter(text, " ") {
		select {
		case <-ctx.Done():
			return provider.ChatOutcome{AccountID: "m1", Attempts: 1, Status: 499}
		case <-time.After(20 * time.Millisecond):
		}
		send(chunk(map[string]any{"content": word}, nil))
	}
	send(chunk(map[string]any{}, "stop"))
	send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
		"choices": []any{}, "usage": usageObj})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
	return provider.ChatOutcome{AccountID: "m1", Attempts: 1, Status: 200, Usage: usage, HasUsage: true}
}
