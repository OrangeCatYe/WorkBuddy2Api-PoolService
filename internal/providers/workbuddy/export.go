package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
)

// ExportAccounts 实现 provider.AccountExporter：导出所选账号的凭证。
//
// 输出是凭证数组，每项与 auths/*.json 的落盘形态一致，因此既能被「添加账号 → 导入凭证」
// 原样接收（importJSON 本就支持凭证数组），也能被旧版本和其他读取 auths 的工具使用。
// 不导出余额、冷却等运行态：导入后会重新拉取。
//
// 内容含令牌明文：鉴权由面板接口负责，这里只记数量，绝不把凭证写进日志。
func (p *Provider) ExportAccounts(_ context.Context, ids []string) (*provider.AccountExport, error) {
	seen := make(map[string]bool, len(ids))
	docs := make([]map[string]any, 0, len(ids))
	var missing []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		a := p.pool.AuthByUID(id)
		if a == nil {
			missing = append(missing, id)
			continue
		}
		doc, err := a.Export()
		if err != nil {
			missing = append(missing, id)
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, errors.New("所选账号都无法导出（不存在或凭证不完整）")
	}
	raw, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return nil, err
	}
	log.Printf("[workbuddy] 导出账号配置 %d 个", len(docs))
	return &provider.AccountExport{Data: raw, Count: len(docs), Missing: missing}, nil
}

// shouldKeepExisting 导入时同一 UID 已在池中，且现有凭证的过期时间不早于导入内容
// 时返回 true（保留现有、跳过导入）。
//
// 为什么：刷新令牌会轮换，用一份旧导出覆盖较新的凭证，会让账号的刷新令牌失效，
// 只能重新授权。与迁移工具（cmd/migrate）「保留令牌较新的一方」的规则一致。
// 导入内容没有过期时间（手写凭证）视为未知，按原行为覆盖。
func shouldKeepExisting(cur, in *auth.Auth) bool {
	if cur == nil || in == nil || in.ExpiresAt <= 0 {
		return false
	}
	cur.Lock()
	exp := cur.ExpiresAt
	cur.Unlock()
	return exp >= in.ExpiresAt
}
