// Package account 定义与具体平台无关的统一账号模型。
//
// 各 Provider 把自己的凭证 / 池状态投影成 Account，面板、积分账本与选号策略
// 只依赖本包，不感知 WorkBuddy 等平台细节；平台特有字段放入 Meta。
package account

import "time"

// Status 账号对外统一状态。
type Status string

const (
	StatusActive   Status = "active"   // 可参与选号
	StatusCooling  Status = "cooling"  // 冷却 / 熔断 / 降权，到期自动恢复
	StatusPaused   Status = "paused"   // 运维暂停选号，账号健康
	StatusDisabled Status = "disabled" // 终态，需人工处理（重登 / 解冻）
	StatusError    Status = "error"    // 凭证异常等
)

// Account 统一账号快照（只读视图，由 Provider 生成）。
type Account struct {
	ID           string         `json:"id"`       // Provider 内唯一
	Provider     string         `json:"provider"` // Provider ID
	Label        string         `json:"label"`    // 昵称 / 邮箱等展示名
	Status       Status         `json:"status"`
	StatusReason string         `json:"status_reason,omitempty"`
	CoolingUntil *time.Time     `json:"cooling_until,omitempty"`
	Quota        *Quota         `json:"quota,omitempty"`
	Stats        Stats          `json:"stats"`
	Tags         []string       `json:"tags,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
}

// Stats 调用统计。
type Stats struct {
	Success    int64      `json:"success"`
	Fail       int64      `json:"fail"`
	InFlight   int        `json:"in_flight"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Quota 额度快照。积分类平台使用 Packages 描述分批到期；
// 周期额度（企业版）使用 CycleResetAt。
type Quota struct {
	Unit         string     `json:"unit"` // 如 "credit"
	Remaining    float64    `json:"remaining"`
	Total        float64    `json:"total"`
	Unlimited    bool       `json:"unlimited,omitempty"`
	Packages     []Package  `json:"packages,omitempty"`
	CycleResetAt *time.Time `json:"cycle_reset_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Package 一批积分（积分包）。
type Package struct {
	ID        string     `json:"id,omitempty"`
	Name      string     `json:"name,omitempty"`
	Source    string     `json:"source,omitempty"` // checkin / task / purchase / cycle / gift ...
	Amount    float64    `json:"amount"`
	Remaining float64    `json:"remaining"`
	ExpireAt  *time.Time `json:"expire_at,omitempty"`
}

// Expired 判断积分包在 now 时刻是否已过期。
func (p Package) Expired(now time.Time) bool {
	return p.ExpireAt != nil && !p.ExpireAt.After(now)
}

// Usable 账号当前是否可承接新请求（仅依据统一状态）。
func (a Account) Usable() bool { return a.Status == StatusActive }

// Key 返回全局唯一键 provider/id。
func (a Account) Key() string { return a.Provider + "/" + a.ID }
