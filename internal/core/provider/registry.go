package provider

import (
	"fmt"
	"sort"
	"sync"
)

// Registry Provider 注册表（并发安全，按注册顺序即优先级）。
type Registry struct {
	mu    sync.RWMutex
	order []string
	items map[string]Provider
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{items: map[string]Provider{}}
}

// Register 注册 Provider；ID 重复返回错误。
func (r *Registry) Register(p Provider) error {
	id := p.Meta().ID
	if id == "" {
		return fmt.Errorf("provider id is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; ok {
		return fmt.Errorf("provider %q already registered", id)
	}
	r.items[id] = p
	r.order = append(r.order, id)
	return nil
}

// Get 按 ID 获取。
func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.items[id]
	return p, ok
}

// List 按注册顺序返回全部 Provider。
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.items[id])
	}
	return out
}

// IDs 返回排序后的 ID 列表（用于稳定输出）。
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := append([]string(nil), r.order...)
	sort.Strings(ids)
	return ids
}

// Priority 返回 Provider 的注册序号（越小优先级越高），不存在返回 -1。
func (r *Registry) Priority(id string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i, v := range r.order {
		if v == id {
			return i
		}
	}
	return -1
}
