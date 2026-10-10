package gateway

import (
	"strings"

	"github.com/orangeruan/poolservice/internal/core/router"
)

// modelCreated /v1/models 的固定 created 时间戳（OpenAI 字段，客户端只做展示）。
const modelCreated = 1760000000

// NamingOptions /v1/models 命名选项。
type NamingOptions struct {
	Style        string // short / full / both
	CreditSuffix bool   // ID 与显示名附加积分倍率
}

// ModelEntries 把目录转换为 /v1/models 条目。
//
// 字段同时满足 OpenAI 规范与 ttswitch 解析（services/model_fetch.rs）。
//
// 注意：ttswitch 用 serde 别名把多个名字映射到同一字段（name/display_name、
// context_window/context_length、reasoning_levels/reasoning_efforts 等），同一对象里
// 同组名字出现两个会触发 serde 的 duplicate field 错误，整个列表解析失败。
// 因此每组只输出一个名字，见 ttswitchAliasGroups 与对应测试。
func ModelEntries(entries []router.Entry, opt NamingOptions) []map[string]any {
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
		switch opt.Style {
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
			out = append(out, modelEntry(id, e, opt.CreditSuffix))
		}
	}
	return out
}

func modelEntry(id string, e router.Entry, creditSuffix bool) map[string]any {
	m := e.Model
	name := m.DisplayName
	if name == "" {
		name = m.ID
	}
	if creditSuffix && m.CreditRateText != "" {
		id = router.DecorateCredit(id, m.CreditRateText)
		name = name + " (" + m.CreditRateText + ")"
	}
	entry := map[string]any{
		"id":           id,
		"object":       "model",
		"created":      modelCreated,
		"owned_by":     e.Provider,
		"provider":     e.Provider,
		"display_name": name,
	}
	if m.Description != "" {
		entry["description"] = m.Description
	}
	if m.Vendor != "" {
		entry["vendor"] = m.Vendor
	}
	if m.ContextWindow > 0 {
		entry["context_window"] = m.ContextWindow
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
	for _, k := range []string{"promo_label", "promo_note", "list_credit_rate", "realm"} {
		if v, ok := m.Extra[k]; ok {
			entry[k] = v
		}
	}
	return entry
}

// ttswitchAliasGroups ttswitch ModelEntry 中映射到同一字段的名字（每组最多出现一个）。
var ttswitchAliasGroups = [][]string{
	{"name", "display_name"},
	{"context_window", "context_length", "max_context_length", "context_window_tokens"},
	{"max_output_tokens", "max_tokens", "max_completion_tokens", "max_output_length", "output_token_limit"},
	{"supports_tools", "tool_call", "supports_tool_calls", "supports_function_calling"},
	{"reasoning_levels", "reasoning_efforts"},
}
