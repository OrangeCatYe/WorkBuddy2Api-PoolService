// Package provider 定义平台适配器（Provider）的能力接口与注册表。
//
// 设计：Provider 只需实现基础接口，其余能力按需实现可选接口；
// 核心层通过类型断言发现能力，面板依据 Meta.Capabilities 渲染入口。
package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/orangeruan/poolservice/internal/core/account"
)

// Capability 可选能力标识，供面板动态渲染。
type Capability string

const (
	CapChat       Capability = "chat"        // 模型目录 + 聊天转发
	CapQuota      Capability = "quota"       // 额度 / 积分查询
	CapLogin      Capability = "login"       // 添加账号
	CapAccountOps Capability = "account_ops" // 账号运维动作
	CapTasks      Capability = "tasks"       // 定时任务 / 成长任务
	CapSettings   Capability = "settings"    // 面板可修改平台参数
	CapExport     Capability = "export"      // 账号配置导出
)

// Meta Provider 元信息。
type Meta struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	Icon         string       `json:"icon,omitempty"` // 面板图标 key 或 URL
	Homepage     string       `json:"homepage,omitempty"`
	Capabilities []Capability `json:"capabilities"`
	// AccountFields 账号表额外展示列（取自 Account.Meta[key]）。
	AccountFields []FieldDef `json:"account_fields,omitempty"`
}

// FieldDef 面板字段描述。
type FieldDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"` // text / number / time / badge / bool
}

// Provider 基础接口。
type Provider interface {
	Meta() Meta
	Accounts() []account.Account
}

// Starter 需要后台协程的 Provider（定时刷新、持久化等）。
type Starter interface {
	Start(ctx context.Context) error
	Stop()
}

// ---- 聊天 ----

// Model Provider 对外声明的模型能力（网关会转换为 ttswitch 兼容字段）。
type Model struct {
	ID               string         `json:"id"` // 上游模型 ID（不含 Provider 前缀）
	DisplayName      string         `json:"display_name,omitempty"`
	Description      string         `json:"description,omitempty"`
	Vendor           string         `json:"vendor,omitempty"`
	ContextWindow    int64          `json:"context_window,omitempty"`
	MaxOutputTokens  int64          `json:"max_output_tokens,omitempty"`
	SupportsImages   bool           `json:"supports_images,omitempty"`
	SupportsTools    bool           `json:"supports_tools,omitempty"`
	SupportsThinking bool           `json:"supports_reasoning,omitempty"`
	ReasoningLevels  []string       `json:"reasoning_levels,omitempty"`
	DefaultReasoning string         `json:"default_reasoning,omitempty"`
	CreditRate       float64        `json:"credit_rate"` // 积分倍率，<0 表示未知
	CreditRateText   string         `json:"credit_rate_text,omitempty"`
	Tags             []string       `json:"tags,omitempty"`
	Aliases          []string       `json:"aliases,omitempty"` // 兼容旧名（如 cn:glm-5.2）
	Extra            map[string]any `json:"extra,omitempty"`
}

// ChatRequest 网关交给 Provider 的规范化请求。
type ChatRequest struct {
	Model           string // 上游模型 ID（已去掉 Provider 前缀与别名）
	RequestedModel  string // 客户端原始模型名
	Body            []byte // OpenAI Chat Completions JSON（model 字段已改写为 Model）
	Stream          bool
	ReasoningEffort string // 已按模型能力映射后的档位，空 = 模型默认
	ConversationKey string // 会话粘性键
	RequestID       string
	ClientIP        string
	UserAgent       string
	KeyID           string // 下游 API Key ID
	Picker          Picker // 核心选号策略（可为 nil，Provider 自行选号）
	Header          http.Header
}

// Usage 规范化用量。
type Usage struct {
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CachedTokens     int64   `json:"cached_tokens,omitempty"`
	ReasoningTokens  int64   `json:"reasoning_tokens,omitempty"`
	Credit           float64 `json:"credit,omitempty"`
	HasCredit        bool    `json:"-"`
}

// ChatOutcome 一次请求的结果摘要（用于账本、日志与观测）。
type ChatOutcome struct {
	AccountID  string
	Attempts   int
	Status     int
	Usage      Usage
	HasUsage   bool
	TTFB       time.Duration
	ErrorKind  string // 失败时的错误分类
	ErrorMsg   string
	Decision   string // 选号决策说明
	UpstreamID string
}

// ChatCapable 聊天能力。ServeChat 直接向 w 写出 OpenAI 兼容响应（SSE 或 JSON），
// 网关在 w 外层做 model 回显、usage 采集与错误规范化。
type ChatCapable interface {
	Models(ctx context.Context) ([]Model, error)
	ServeChat(ctx context.Context, req *ChatRequest, w http.ResponseWriter) ChatOutcome
}

// Candidate 选号候选。
type Candidate struct {
	AccountID string
	Quota     *account.Quota
	InFlight  int
	LastUsed  time.Time
	Sticky    bool    // 会话粘性绑定账号
	CostTier  int     // 0 实测免费 / 1 未知 / 2 收费
	Weight    float64 // Provider 侧基础权重（兼容既有算法）
}

// Ranked 排序结果。
type Ranked struct {
	AccountID string
	Score     float64
	Reason    string
}

// Picker 由核心注入的选号策略。
type Picker interface {
	Rank(provider, model string, cands []Candidate) []Ranked
}

// ---- 额度 ----

// QuotaCapable 额度查询能力。
type QuotaCapable interface {
	RefreshQuota(ctx context.Context, accountID string) (*account.Quota, error)
}

// ---- 登录 ----

// LoginMethod 登录方式描述。
type LoginMethod struct {
	ID          string     `json:"id"`   // oauth_device / import_json / api_key ...
	Name        string     `json:"name"` // 展示名
	Description string     `json:"description,omitempty"`
	Fields      []FieldDef `json:"fields,omitempty"` // 需要用户填写的字段
}

// LoginSession 登录会话。
type LoginSession struct {
	ID        string    `json:"id"`
	AuthURL   string    `json:"auth_url,omitempty"`
	UserCode  string    `json:"user_code,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LoginStatus 登录轮询结果。
type LoginStatus struct {
	State     string `json:"state"` // pending / success / failed / expired
	Message   string `json:"message,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

// LoginCapable 添加账号能力。
type LoginCapable interface {
	LoginMethods() []LoginMethod
	StartLogin(ctx context.Context, method string, params map[string]string) (*LoginSession, *LoginStatus, error)
	PollLogin(ctx context.Context, sessionID string) (*LoginStatus, error)
}

// ---- 账号运维 ----

// ActionDef 账号动作描述。
type ActionDef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Danger  bool   `json:"danger,omitempty"`  // 危险操作需确认
	Bulk    bool   `json:"bulk,omitempty"`    // 支持批量
	Confirm string `json:"confirm,omitempty"` // 确认文案
}

// ActionResult 动作执行结果。
type ActionResult struct {
	OK      bool           `json:"ok"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// AccountOps 账号运维能力。
type AccountOps interface {
	Actions() []ActionDef
	Do(ctx context.Context, accountID, action string, params map[string]string) (*ActionResult, error)
}

// ---- 账号导出 ----

// AccountExport 账号配置导出结果。
type AccountExport struct {
	// Data 可直接保存为文件的 JSON，必须能被同一平台的导入方式（LoginCapable 里的
	// import 类方法）原样接收。
	Data json.RawMessage `json:"accounts"`
	// Count 实际导出的账号数。
	Count int `json:"count"`
	// Missing 请求了但没有导出的账号 ID（不存在或凭证不完整）。
	Missing []string `json:"missing,omitempty"`
}

// AccountExporter 账号配置导出能力。导出内容含登录凭证明文，调用方负责鉴权，
// 实现方不得把凭证写进日志。
type AccountExporter interface {
	// ExportAccounts 导出指定账号。无一可导出时返回错误；部分缺失记入 Missing。
	ExportAccounts(ctx context.Context, accountIDs []string) (*AccountExport, error)
}

// ---- 任务 ----

// TaskDef 定时 / 手动任务描述。
type TaskDef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Hours       []int  `json:"hours,omitempty"` // 默认整点
	Enabled     bool   `json:"enabled"`
}

// TaskCapable 任务能力。
type TaskCapable interface {
	Tasks() []TaskDef
	RunTask(ctx context.Context, taskID string, accountIDs []string) error
}

// ---- 路由调优（积分策略） ----

// PickCandidate 选号候选及其最终权重。
type PickCandidate struct {
	AccountID string  `json:"account_id"`
	Weight    float64 `json:"weight"`
	Bias      float64 `json:"bias"`
	Tier      int     `json:"tier"` // 0 实测免费 / 1 未知 / 2 实测收费
}

// PickEvent 一次选号。
type PickEvent struct {
	Time       time.Time       `json:"time"`
	Provider   string          `json:"provider"`
	Model      string          `json:"model"`
	Chosen     string          `json:"chosen"`
	Fallback   bool            `json:"fallback,omitempty"`
	Sticky     bool            `json:"sticky,omitempty"`
	Explored   bool            `json:"explored,omitempty"`
	Candidates []PickCandidate `json:"candidates,omitempty"`
}

// RoutingTunable 允许核心积分策略影响选号的 Provider。
// bias 返回账号权重乘数（1 = 不变，0 = 有其他候选时不选）；两个回调都可能在
// Provider 内部锁中调用，必须快速返回且不得回调 Provider。
type RoutingTunable interface {
	SetRoutingBias(fn func(accountID, model string) float64)
	SetPickObserver(fn func(PickEvent))
	// SetCreditReserve 余额保底：低于该值的账号不接收费模型；0 = 沿用 Provider 自身配置。
	SetCreditReserve(n float64)
}

// MetaWithCaps 根据实际实现的接口补全能力清单。
func MetaWithCaps(p Provider) Meta {
	m := p.Meta()
	has := map[Capability]bool{}
	for _, c := range m.Capabilities {
		has[c] = true
	}
	add := func(c Capability, ok bool) {
		if ok && !has[c] {
			m.Capabilities = append(m.Capabilities, c)
			has[c] = true
		}
	}
	_, chat := p.(ChatCapable)
	_, quota := p.(QuotaCapable)
	_, login := p.(LoginCapable)
	_, ops := p.(AccountOps)
	_, tasks := p.(TaskCapable)
	_, settings := p.(SettingsCapable)
	_, quests := p.(QuestCapable)
	_, export := p.(AccountExporter)
	add(CapChat, chat)
	add(CapQuota, quota)
	add(CapLogin, login)
	add(CapAccountOps, ops)
	add(CapTasks, tasks)
	add(CapSettings, settings)
	add(CapQuests, quests)
	add(CapExport, export)
	return m
}
