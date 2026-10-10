package workbuddy

import (
	"strings"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/pool"
)

var _ provider.RoutingTunable = (*Provider)(nil)

// bareModel engine 选号使用的模型名可能带 realm 前缀（cn: / global:），统一去掉。
func bareModel(m string) string {
	if i := strings.IndexByte(m, ':'); i > 0 {
		return m[i+1:]
	}
	return m
}

// SetRoutingBias 实现 provider.RoutingTunable。
func (p *Provider) SetRoutingBias(fn func(accountID, model string) float64) {
	if fn == nil {
		p.pool.SetRoutingBias(nil)
		return
	}
	p.pool.SetRoutingBias(func(uid, model string) float64 { return fn(uid, bareModel(model)) })
}

// SetPickObserver 实现 provider.RoutingTunable。
func (p *Provider) SetPickObserver(fn func(provider.PickEvent)) {
	if fn == nil {
		p.pool.SetPickObserver(nil)
		return
	}
	p.pool.SetPickObserver(func(e pool.PickEvent) {
		ev := provider.PickEvent{Time: e.Time, Provider: ID, Model: bareModel(e.Model), Chosen: e.Chosen, Fallback: e.Fallback, Sticky: e.Sticky, Explored: e.Explored}
		if len(e.Candidates) > 0 {
			ev.Candidates = make([]provider.PickCandidate, len(e.Candidates))
			for i, c := range e.Candidates {
				ev.Candidates[i] = provider.PickCandidate{AccountID: c.UID, Weight: c.Weight, Bias: c.Bias, Tier: c.Tier}
			}
		}
		fn(ev)
	})
}

// SetCreditReserve 实现 provider.RoutingTunable：>0 覆盖 pool.credit_floor，0 恢复配置值。
func (p *Provider) SetCreditReserve(n float64) {
	if n > 0 {
		p.pool.SetCreditFloor(int64(n))
		return
	}
	p.pool.SetCreditFloor(p.cfg.Pool.CreditFloor)
}
