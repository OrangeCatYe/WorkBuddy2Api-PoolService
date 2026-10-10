// PoolService 扩展：外部路由权重系数与选号观察回调。
//
// 这两个扩展点让核心层的积分策略（快过期优先、按倍率分配、节流）作用于原项目的
// 选号算法，而不改变其冷却 / 熔断 / 成本分层 / 防撞号等既有语义：
//   - 权重系数只乘在加权抽签的权重上，候选集过滤与成本分层不受影响；
//   - 未设置时（nil）行为与原项目逐字节一致。
package pool

import "time"

// PickCandidate 一次选号中进入最终加权层的候选。
type PickCandidate struct {
	UID    string
	Weight float64 // 含外部系数后的最终权重
	Bias   float64 // 外部系数（未设置时为 1）
	Tier   int     // 成本层：0 实测免费 / 1 未知 / 2 实测收费
}

// PickEvent 选号事件。
type PickEvent struct {
	Time       time.Time
	Model      string
	Realm      string
	Chosen     string
	Fallback   bool // 全冷却兜底
	Sticky     bool // 会话粘性命中（未经过加权选号）
	Explored   bool // 成本探索改道
	Candidates []PickCandidate
}

// SetRoutingBias 注入外部权重系数 fn(uid, model) → 乘数（≥0）。nil = 关闭。
// fn 在持 p.mu 时调用，必须快速返回且不得反向调用 Pool 方法。
func (p *Pool) SetRoutingBias(fn func(uid, model string) float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routingBias = fn
}

// SetPickObserver 注入选号观察回调。nil = 关闭。
// fn 在持 p.mu 时调用，必须快速返回（如写入环形缓冲）且不得反向调用 Pool 方法。
func (p *Pool) SetPickObserver(fn func(PickEvent)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pickObserver = fn
}

// biasOf 外部系数（未设置 / 非法值时为 1）。调用方需持 p.mu。
func (p *Pool) biasOf(e *entry, model string) float64 {
	if p.routingBias == nil {
		return 1
	}
	b := p.routingBias(e.a.UID, model)
	if b < 0 || b != b { // 负数或 NaN
		return 1
	}
	return b
}
