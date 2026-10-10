package router

import "strings"

// effortOrder 推理强度的全序（ttswitch 五档 + 上游常见扩展）。
var effortOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func effortRank(e string) int {
	for i, v := range effortOrder {
		if v == e {
			return i
		}
	}
	return -1
}

// NormalizeEffort 统一大小写与别名（off→none，maximum→max，extra_high→xhigh）。
// auto / default 归一为空串，表示「使用模型默认档」。
func NormalizeEffort(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	switch e {
	case "off", "disabled", "disable":
		return "none"
	case "maximum", "ultra":
		return "max"
	case "extra_high", "extra-high", "x-high", "very_high":
		return "xhigh"
	case "med", "normal":
		return "medium"
	case "auto", "default":
		return ""
	}
	return e
}

// MapEffort 把客户端请求的档位映射到模型实际支持的档位。
//
// 规则（不静默丢弃意图）：
//  1. 空 / auto → 返回空，交由模型默认档
//  2. 支持集合为空或请求档位未知 → 归一后原样返回（不猜测，透传上游）
//  3. 命中 → 原样
//  4. 未命中 → 取不高于请求档位的最高支持档；不存在则取最低支持档
//     （请求非 none 时，不会被映射成 none，避免把「要思考」变成「关思考」）
func MapEffort(requested string, supported []string) (applied string, mapped bool) {
	req := NormalizeEffort(requested)
	rq := effortRank(req)
	if req == "" || len(supported) == 0 || rq < 0 {
		return req, false
	}
	best, bestRank := "", -1
	low, lowRank := "", len(effortOrder)
	for _, s := range supported {
		s = NormalizeEffort(s)
		r := effortRank(s)
		if r < 0 || (s == "none" && req != "none") {
			continue
		}
		if s == req {
			return req, false
		}
		if r <= rq && r > bestRank {
			best, bestRank = s, r
		}
		if r < lowRank {
			low, lowRank = s, r
		}
	}
	if best != "" {
		return best, true
	}
	if low != "" {
		return low, true
	}
	return req, false
}
