package gateway

import (
	"strings"

	"github.com/orangeruan/poolservice/internal/core/router"
)

// modelCreated /v1/models 的固定 created 时间戳（OpenAI 字段，客户端只做展示）。
const modelCreated = 1760000000

// ModelEntries 把目录转换为 /v1/models 条目。
//
// 字段同时满足 OpenAI 规范与 ttswitch 解析（services/model_fetch.rs）：
// display_name、context_window、max_output_tokens、input_modalities、
// supports_tools、reasoning_levels 是 ttswitch 识别的规范名；
// context_length、name、reasoning_efforts 作为通用别名一并输出。
func ModelEntries(entries []router.Entry, naming string) []map[string]any {
	// 短名冲突检测：同一短名只给优先级最高者（entries 已按注册顺序）。
	shortOwner := map[string]int{}
	for i, e := range entries {
		k := strings.ToLower(e.Model.ID)
		if _, ok := shortOwner[k]; !ok {
			shortOwner[k] = i
		}
	}
	out := make([]map[string]any, 0, len(entries)*2)
	for i, e := range entries {
		var ids []string
		short := shortOwner[strings.ToLower(e.Model.ID)] == i
		switch naming {
		case "full":
			ids = []string{e.FullID()}
		case "both":
			if short {
				ids = append(ids, e.Model.ID)
			}
			ids = append(ids, e.FullID())
		default: // short
			if short {
				ids = []string{e.Model.ID}
			} else {
				ids = []string{e.FullID()}
			}
		}
		for _, id := range ids {
			out = append(out, modelEntry(id, e))
		}
	}
	return out
}

func modelEntry(id string, e router.Entry) map[string]any {
	m := e.Model
	entry := map[string]any{
		"id":       id,
		"object":   "model",
		"created":  modelCreated,
		"owned_by": e.Provider,
		"provider": e.Provider,
	}
	name := m.DisplayName
	if name == "" {
		name = m.ID
	}
	entry["display_name"] = name
	entry["name"] = name
	if m.Description != "" {
		entry["description"] = m.Description
	}
	if m.Vendor != "" {
		entry["vendor"] = m.Vendor
	}
	if m.ContextWindow > 0 {
		entry["context_window"] = m.ContextWindow
		entry["context_length"] = m.ContextWindow
	}
	if m.MaxOutputTokens > 0 {
		entry["max_output_tokens"] = m.MaxOutputTokens
	}
	if m.SupportsImages {
		entry["input_modalities"] = []string{"text", "image"}
	} else {
		entry["input_modalities"] = []string{"text"}
	}
	entry["output_modalities"] = []string{"text"}
	entry["supports_tools"] = m.SupportsTools
	if m.SupportsThinking || len(m.ReasoningLevels) > 0 {
		entry["supports_reasoning"] = true
	}
	if len(m.ReasoningLevels) > 0 {
		entry["reasoning_levels"] = m.ReasoningLevels
		entry["reasoning_efforts"] = m.ReasoningLevels
	}
	if m.DefaultReasoning != "" {
		entry["reasoning_default_effort"] = m.DefaultReasoning
	}
	if m.CreditRate >= 0 {
		entry["credit_rate"] = m.CreditRate
	}
	if m.CreditRateText != "" {
		entry["credits"] = m.CreditRateText
	}
	if len(m.Tags) > 0 {
		entry["tags"] = m.Tags
	}
	return entry
}
