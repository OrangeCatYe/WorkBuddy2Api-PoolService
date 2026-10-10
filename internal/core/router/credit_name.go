package router

import (
	"regexp"
	"strings"
)

// creditSuffix 匹配模型名末尾的积分倍率标记：glm-5.2(x0.05) / glm-5.2 [x0.5] / glm-5.2(免费)。
var creditSuffix = regexp.MustCompile(`\s*[\(\[（](?:x|×)?\d+(?:\.\d+)?x?(?:\s*credits?)?[\)\]）]\s*$|\s*[\(\[（]免费[\)\]）]\s*$`)

// DecorateCredit 在模型 ID 末尾附加倍率标记，便于在 Agent 的模型选择器里直接看到成本。
// rateText 为空时原样返回。
func DecorateCredit(id, rateText string) string {
	if rateText == "" {
		return id
	}
	return id + "(" + rateText + ")"
}

// StripCreditSuffix 去掉倍率标记。倍率会随限时优惠变化，客户端里保存的旧名
// （如 glm-5.2(x0.05)）在倍率变成 x0.1 后仍能解析到同一模型。
func StripCreditSuffix(name string) string {
	return strings.TrimSpace(creditSuffix.ReplaceAllString(name, ""))
}
