// Package router 聚合各 Provider 的模型目录，并把客户端模型名解析到具体 Provider。
//
// 命名规则：
//   - 全名 `provider/model`（如 workbuddy/glm-5.2）始终唯一
//   - 短名 `model`：仅一个 Provider 提供时直达；多个 Provider 同名时取注册优先级最高者
//   - 别名：Provider 声明的兼容旧名（如 cn:glm-5.2）
//
// 名称匹配大小写不敏感。
package router

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

// Entry 目录中的一个模型。
type Entry struct {
	Provider string
	Model    provider.Model
}

// FullID 返回 provider/model。
func (e Entry) FullID() string { return e.Provider + "/" + e.Model.ID }

// Catalog 模型目录（带缓存）。
type Catalog struct {
	reg *provider.Registry
	ttl time.Duration

	mu        sync.RWMutex
	entries   []Entry
	byName    map[string][]int // 小写名 -> entries 下标（按优先级排序）
	fetchedAt time.Time
	errs      map[string]string
	refreshMu sync.Mutex
}

// NewCatalog 创建目录。ttl<=0 时默认 10 分钟。
func NewCatalog(reg *provider.Registry, ttl time.Duration) *Catalog {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Catalog{reg: reg, ttl: ttl, byName: map[string][]int{}, errs: map[string]string{}}
}

// Entries 返回目录快照（必要时刷新）。
func (c *Catalog) Entries(ctx context.Context) []Entry {
	c.ensureFresh(ctx)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Entry(nil), c.entries...)
}

// Errors 返回最近一次刷新中各 Provider 的错误。
func (c *Catalog) Errors() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.errs))
	for k, v := range c.errs {
		out[k] = v
	}
	return out
}

// Invalidate 使缓存失效。
func (c *Catalog) Invalidate() {
	c.mu.Lock()
	c.fetchedAt = time.Time{}
	c.mu.Unlock()
}

func (c *Catalog) ensureFresh(ctx context.Context) {
	c.mu.RLock()
	fresh := !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl
	c.mu.RUnlock()
	if fresh {
		return
	}
	c.Refresh(ctx)
}

// Refresh 强制从全部 Provider 拉取目录。单个 Provider 失败时保留其上一次结果。
func (c *Catalog) Refresh(ctx context.Context) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	c.mu.RLock()
	prev := map[string][]Entry{}
	for _, e := range c.entries {
		prev[e.Provider] = append(prev[e.Provider], e)
	}
	c.mu.RUnlock()

	var entries []Entry
	errs := map[string]string{}
	for _, p := range c.reg.List() {
		chat, ok := p.(provider.ChatCapable)
		if !ok {
			continue
		}
		pid := p.Meta().ID
		models, err := chat.Models(ctx)
		if err != nil {
			errs[pid] = err.Error()
			entries = append(entries, prev[pid]...)
			continue
		}
		for _, m := range models {
			if strings.TrimSpace(m.ID) == "" {
				continue
			}
			entries = append(entries, Entry{Provider: pid, Model: m})
		}
	}
	c.install(entries, errs)
}

// install 建立名称索引（供测试直接注入）。
func (c *Catalog) install(entries []Entry, errs map[string]string) {
	byName := map[string][]int{}
	add := func(name string, idx int) {
		k := strings.ToLower(strings.TrimSpace(name))
		if k == "" {
			return
		}
		for _, v := range byName[k] {
			if v == idx {
				return
			}
		}
		byName[k] = append(byName[k], idx)
	}
	for i, e := range entries {
		add(e.FullID(), i)
		add(e.Model.ID, i)
		for _, a := range e.Model.Aliases {
			add(a, i)
		}
	}
	for k := range byName {
		idxs := byName[k]
		sort.SliceStable(idxs, func(a, b int) bool {
			return c.reg.Priority(entries[idxs[a]].Provider) < c.reg.Priority(entries[idxs[b]].Provider)
		})
	}
	c.mu.Lock()
	c.entries = entries
	c.byName = byName
	c.errs = errs
	c.fetchedAt = time.Now()
	c.mu.Unlock()
}

// ErrUnknownModel 模型不存在。
type ErrUnknownModel struct{ Name string }

func (e ErrUnknownModel) Error() string { return fmt.Sprintf("model %q not found", e.Name) }

// Resolve 把客户端模型名解析为目录条目。
func (c *Catalog) Resolve(ctx context.Context, name string) (Entry, error) {
	c.ensureFresh(ctx)
	k := strings.ToLower(strings.TrimSpace(name))
	c.mu.RLock()
	idxs := c.byName[k]
	var e Entry
	if len(idxs) > 0 {
		e = c.entries[idxs[0]]
	}
	c.mu.RUnlock()
	if len(idxs) == 0 {
		// 目录可能过期（新模型上线）：强刷一次再试。
		c.Refresh(ctx)
		c.mu.RLock()
		defer c.mu.RUnlock()
		if idxs = c.byName[k]; len(idxs) == 0 {
			return Entry{}, ErrUnknownModel{Name: name}
		}
		return c.entries[idxs[0]], nil
	}
	return e, nil
}
