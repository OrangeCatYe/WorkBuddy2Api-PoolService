package upstream

import "testing"

// 2026-10-10 两路目录实测形态：v3 输出偏大且有大于上下文的值。
func TestReconcileOutputLimits(t *testing.T) {
	merged := []ModelInfo{
		{ID: "kimi-k3-1", ContextWindow: 1000000, MaxTokens: 1048576},    // v3：输出 > 上下文
		{ID: "minimax-m3", ContextWindow: 512000, MaxTokens: 524288},     // v3：输出 > 上下文
		{ID: "glm-5.2", ContextWindow: 1000000, MaxTokens: 131072},       // v3 偏大
		{ID: "glm-5.3-flash", ContextWindow: 1000000, MaxTokens: 131072}, // 两路一致
		{ID: "only-v3", ContextWindow: 100000, MaxTokens: 200000},        // 企业端点无此模型 → 兜底
		{ID: "glm-5v-turbo", ContextWindow: 200000, MaxTokens: 0},        // 仅企业端点 / 缺值
	}
	enterprise := []ModelInfo{
		{ID: "kimi-k3-1", MaxTokens: 32000},
		{ID: "minimax-m3", MaxTokens: 64000},
		{ID: "glm-5.2", MaxTokens: 64000},
		{ID: "glm-5.3-flash", MaxTokens: 131072},
		{ID: "glm-5v-turbo", MaxTokens: 64000},
	}
	want := map[string]int64{"kimi-k3-1": 32000, "minimax-m3": 64000, "glm-5.2": 64000, "glm-5.3-flash": 131072, "only-v3": 50000, "glm-5v-turbo": 64000}
	for _, m := range reconcileOutputLimits(merged, enterprise) {
		if m.MaxTokens != want[m.ID] {
			t.Errorf("%s: MaxTokens=%d want %d", m.ID, m.MaxTokens, want[m.ID])
		}
		if m.ContextWindow > 0 && m.MaxTokens >= m.ContextWindow {
			t.Errorf("%s: output %d >= context %d", m.ID, m.MaxTokens, m.ContextWindow)
		}
	}
}

// 静态兜底表不允许出现输出不小于上下文的条目。
func TestContextCapFallbackSane(t *testing.T) {
	for id, c := range contextCapFallback {
		if c.maxOutput >= c.context {
			t.Errorf("%s: maxOutput %d >= context %d", id, c.maxOutput, c.context)
		}
	}
}
