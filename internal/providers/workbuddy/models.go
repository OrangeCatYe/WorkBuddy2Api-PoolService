package workbuddy

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/upstream"
)

// globalPrefix 国际版模型 ID 前缀（与原项目路由协议一致）。CN 模型输出裸名，
// 同时登记 "cn:<id>" 别名兼容旧客户端配置。
const globalPrefix = "global:"

// Models 拉取 CN（及启用时的国际版）模型目录。
// 上游缓存与负缓存在 engine 内部（10min / 5min），这里不再额外节流。
func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	var out []provider.Model
	var errs []string

	if a := p.firstAccount("cn"); a != nil {
		infos, err := p.up.FetchModels(a)
		if err != nil {
			errs = append(errs, "cn: "+err.Error())
		}
		for _, mi := range infos {
			out = append(out, p.toModel("cn", mi.ID, mi))
		}
	}
	if p.up.GlobalEnabled && ctx.Err() == nil {
		if a := p.firstAccount("global"); a != nil {
			for _, mi := range p.up.FetchGlobalModelInfos(a) {
				out = append(out, p.toModel("global", globalPrefix+mi.ID, mi))
			}
		}
	}
	if len(out) == 0 {
		if len(errs) > 0 {
			return nil, errors.New(strings.Join(errs, "; "))
		}
		if len(p.pool.List()) == 0 {
			return nil, nil // 无账号：空目录不是错误
		}
		return nil, errors.New("no available account to fetch model catalog")
	}
	return out, nil
}

func (p *Provider) toModel(realm, id string, mi upstream.ModelInfo) provider.Model {
	httpc := p.up.HTTP
	m := provider.Model{
		ID:               id,
		DisplayName:      mi.Name,
		Description:      mi.Description,
		Vendor:           mi.Vendor,
		ContextWindow:    upstream.ContextWindowListingV4(mi.ID, mi.ContextWindow, httpc),
		SupportsImages:   mi.SupportsImages,
		SupportsTools:    mi.SupportsToolCall,
		SupportsThinking: mi.SupportsReasoning,
		Tags:             append([]string(nil), mi.Tags...),
		CreditRate:       -1,
		Extra:            map[string]any{"realm": realm},
	}
	if mo, ok := upstream.MaxOutputTokensListingV4(mi.ID, mi.MaxTokens, httpc); ok {
		m.MaxOutputTokens = mo
	}
	var remoteEfforts []string
	var remoteDefault string
	if realm == "global" {
		effs, defs := p.up.GlobalEffortSnapshot()
		remoteEfforts, remoteDefault = effs[mi.ID], defs[mi.ID]
	} else {
		remoteEfforts, remoteDefault = mi.Efforts, mi.DefaultEffort
	}
	if efforts, def := upstream.EffortListing(realm, mi.ID, remoteEfforts, remoteDefault); efforts != nil {
		m.ReasoningLevels = efforts
		m.DefaultReasoning = def
		m.SupportsThinking = true
	}
	if mi.CanDisableThinking {
		m.Extra["can_disable_thinking"] = true
	}
	if realm == "cn" {
		m.Aliases = []string{"cn:" + mi.ID}
	}

	// 积分倍率：牌价 credits，限时优惠生效时取折后价（PromoFactor）。
	if base, ok := ParseCreditRate(mi.Credits); ok {
		m.CreditRate = base
		if mi.PromoFactor != nil {
			m.CreditRate = base * *mi.PromoFactor
			m.Extra["list_credit_rate"] = base
		}
		m.CreditRateText = FormatCreditRate(m.CreditRate)
	}
	if mi.PromoLabel != "" {
		m.Extra["promo_label"] = mi.PromoLabel
		m.Tags = append(m.Tags, mi.PromoLabel)
	}
	if mi.PromoNote != "" {
		m.Extra["promo_note"] = mi.PromoNote
	}
	return m
}

// ParseCreditRate 解析上游倍率原文："x0.05 credits" / "x0.29" / "0.50x" / "0x"。
func ParseCreditRate(raw string) (float64, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimSpace(strings.TrimSuffix(s, "credits"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "credit"))
	s = strings.Trim(s, "x× ")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// FormatCreditRate 统一展示为 "x0.05"（最多 4 位小数，去掉多余的 0）。
func FormatCreditRate(v float64) string {
	return "x" + strconv.FormatFloat(math.Round(v*1e4)/1e4, 'f', -1, 64)
}
