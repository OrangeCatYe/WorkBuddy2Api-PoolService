package provider

import (
	"context"

	"github.com/orangeruan/poolservice/internal/core/taskrun"
)

// CapQuests 账号级任务（成长任务 / 积分任务）。
const CapQuests Capability = "quests"

// TaskHistoryAware 需要记录任务执行历史的平台：核心层在注册后注入记录器。
// 平台每次执行定时 / 手动任务时用 Begin 开一条记录，逐账号写入结果。
type TaskHistoryAware interface {
	SetTaskRecorder(r *taskrun.Recorder)
}

// Quest 状态。
const (
	QuestLocked    = "locked"    // 上游未解锁
	QuestTodo      = "todo"      // 未完成
	QuestClaimable = "claimable" // 已达标，待领奖
	QuestClaimed   = "claimed"   // 已领奖
)

// Quest 账号上的一个任务。
type Quest struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Credit      int64  `json:"credit,omitempty"` // 奖励积分
	Energy      int64  `json:"energy,omitempty"` // 其他奖励（如能量）
	Current     int64  `json:"current"`
	Target      int64  `json:"target"`
	Status      string `json:"status"`
	Accepted    bool   `json:"accepted"`
	Tag         string `json:"tag,omitempty"` // 端标记（PC / 小程序 …）
	// Auto 可自动完成；AutoDesc 说明自动完成的做法；Attempt 尝试型（不保证点亮）。
	Auto     bool   `json:"auto"`
	AutoDesc string `json:"auto_desc,omitempty"`
	Attempt  bool   `json:"attempt,omitempty"`
}

// QuestClaim 领奖结果。
type QuestClaim struct {
	Credit  int64 `json:"credit"`
	Energy  int64 `json:"energy"`
	Already bool  `json:"already"` // 此前已领取
}

// QuestCapable 账号级任务能力（面板账号详情的「任务」页签）。
type QuestCapable interface {
	Quests(ctx context.Context, accountID string) ([]Quest, error)
	// StartQuests 异步自动完成任务：codes 为空 = 该账号全部待办。返回执行记录 ID，面板据此轮询进度。
	StartQuests(accountID string, codes []string) (runID string, err error)
	ClaimQuest(ctx context.Context, accountID, code string) (QuestClaim, error)
	// AcceptQuests 接受全部尚未接受的任务，返回接受数。
	AcceptQuests(ctx context.Context, accountID string) (int, error)
}
