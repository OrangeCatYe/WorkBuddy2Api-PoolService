package workbuddy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
)

// ---- 额度 ----

// RefreshQuota 立即刷新余额与积分包明细。
func (p *Provider) RefreshQuota(ctx context.Context, uid string) (*account.Quota, error) {
	a := p.pool.AuthByUID(uid)
	if a == nil {
		return nil, fmt.Errorf("account %s not found", uid)
	}
	remain, total, expiring, earliestAt, earliestRemain, err := p.up.UserResourceDetailedWithExpiry(a, p.cfg.expiringSoon)
	if err != nil {
		return nil, fmt.Errorf("查询余额失败: %w", err)
	}
	p.pool.SetCreditsDetailed(uid, remain, total, expiring, earliestAt, earliestRemain)
	p.pool.ReenableIfCredits(uid, remain, total)
	p.refreshPackages(uid)
	st, ok := p.pool.Status(uid)
	if !ok {
		return nil, fmt.Errorf("account %s not found", uid)
	}
	return p.quotaOf(st), nil
}

// refreshPackages 拉取单账号积分包明细并缓存。
func (p *Provider) refreshPackages(uid string) {
	a := p.pool.AuthByUID(uid)
	if a == nil {
		return
	}
	items, _, _, err := p.up.CreditPackages(a)
	p.pkgMu.Lock()
	defer p.pkgMu.Unlock()
	if err != nil {
		prev := p.packages[uid]
		prev.err = err.Error()
		p.packages[uid] = prev // 失败保留上次明细
		return
	}
	p.packages[uid] = pkgSnapshot{items: items, at: time.Now()}
}

// packageLoop 周期刷新全部账号积分包明细（逐个、间隔发起，避免并发打上游）。
func (p *Provider) packageLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	t := time.NewTicker(p.cfg.packageRefresh)
	defer t.Stop()
	for {
		for _, st := range p.pool.List() {
			if st.Disabled {
				continue
			}
			p.refreshPackages(st.UID)
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- 账号运维 ----

// Actions 账号动作清单。
func (p *Provider) Actions() []provider.ActionDef {
	return []provider.ActionDef{
		{ID: "refresh", Name: "刷新余额", Bulk: true},
		{ID: "checkin", Name: "签到", Bulk: true},
		{ID: "pause", Name: "暂停选号", Bulk: true},
		{ID: "resume", Name: "恢复选号", Bulk: true},
		{ID: "revive", Name: "解冻", Bulk: true},
		{ID: "disable", Name: "禁用", Bulk: true, Danger: true, Confirm: "禁用后不再参与选号，需手动解冻恢复。"},
		{ID: "remove", Name: "删除", Danger: true, Confirm: "将从池中移除并删除本地凭证文件，此操作不可撤销。"},
	}
}

// Do 执行账号动作。
func (p *Provider) Do(ctx context.Context, uid, action string, _ map[string]string) (*provider.ActionResult, error) {
	a := p.pool.AuthByUID(uid)
	if a == nil {
		return nil, fmt.Errorf("account %s not found", uid)
	}
	switch action {
	case "refresh":
		q, err := p.RefreshQuota(ctx, uid)
		if err != nil {
			return nil, err
		}
		return &provider.ActionResult{OK: true, Message: fmt.Sprintf("余额 %.0f / %.0f", q.Remaining, q.Total)}, nil
	case "checkin":
		if a.Realm() == "global" {
			return nil, errors.New("国际版账号没有签到体系")
		}
		if a.IsEnterprise() {
			return nil, errors.New("企业版账号不支持签到")
		}
		if err := p.up.DailyCheckin(a); err != nil {
			return nil, fmt.Errorf("签到失败: %w", err)
		}
		p.pool.NoteCheckinDone(uid)
		_, _ = p.RefreshQuota(ctx, uid)
		return &provider.ActionResult{OK: true, Message: "签到成功"}, nil
	case "pause":
		p.pool.Pause(uid)
		return &provider.ActionResult{OK: true, Message: "已暂停选号（保号任务照常执行）"}, nil
	case "resume":
		p.pool.Resume(uid)
		return &provider.ActionResult{OK: true, Message: "已恢复选号"}, nil
	case "revive":
		p.pool.Revive(uid)
		return &provider.ActionResult{OK: true, Message: "已清除冷却 / 熔断 / 禁用状态"}, nil
	case "disable":
		p.pool.Disable(uid, "面板手动禁用")
		return &provider.ActionResult{OK: true, Message: "已禁用"}, nil
	case "remove":
		removed := p.pool.Remove(uid)
		if removed != nil && removed.FilePath != "" {
			if err := os.Remove(removed.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("已移出池，但删除凭证文件失败: %w", err)
			}
		}
		p.pkgMu.Lock()
		delete(p.packages, uid)
		p.pkgMu.Unlock()
		log.Printf("[workbuddy] 账号已删除 uid=%s", shortUID(uid))
		return &provider.ActionResult{OK: true, Message: "已删除"}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

// ---- 定时任务 ----

// Tasks 定时任务清单（时点与开关来自配置）。
func (p *Provider) Tasks() []provider.TaskDef {
	s := p.cfg.Schedule
	return []provider.TaskDef{
		{ID: "checkin", Name: "每日签到", Description: "签到领积分，并刷新余额、解冻已回血账号", Hours: s.CheckinHours, Enabled: s.CheckinEnabled},
		{ID: "activity", Name: "活跃上报", Description: "点亮连登，解锁首个伙伴", Hours: s.ActivityHours, Enabled: s.ActivityEnabled},
		{ID: "travel", Name: "猫猫旅行", Description: "领养、派出、领奖", Hours: s.TravelHours, Enabled: s.TravelEnabled},
		{ID: "keepalive", Name: "Token 保活", Description: "提前刷新访问令牌，避免过期掉线", Hours: s.KeepaliveHours, Enabled: s.KeepaliveEnabled},
		{ID: "blackcat", Name: "夜猫子", Description: "23:00–08:00 窗口对话补足", Hours: s.BlackcatHours, Enabled: s.BlackcatEnabled},
		{ID: "streak", Name: "连登奖励", Description: "连登管家兑换与抽奖", Enabled: s.CheckinEnabled},
		{ID: "balance", Name: "余额刷新", Description: fmt.Sprintf("每 %d 分钟刷新余额", s.BalanceRefreshMinutes), Enabled: s.BalanceRefreshEnabled},
		{ID: "packages", Name: "积分包明细", Description: fmt.Sprintf("每 %d 分钟刷新到期分布", s.PackageRefreshMinutes), Enabled: true},
	}
}

// RunTask 立即执行任务（异步，作用于全部账号；accountIDs 暂未使用）。
func (p *Provider) RunTask(_ context.Context, taskID string, _ []string) error {
	var fn func()
	switch taskID {
	case "checkin":
		fn = p.sched.RunCheckinNow
	case "activity":
		fn = p.sched.RunActivityNow
	case "travel":
		fn = p.sched.RunTravelNow
	case "keepalive":
		fn = p.sched.RunKeepaliveNow
	case "blackcat":
		fn = p.sched.RunBlackcatNow
	case "streak":
		fn = p.sched.RunStreakBonusNow
	case "balance":
		fn = p.sched.RunBalanceRefreshNow
	case "packages":
		fn = func() {
			for _, st := range p.pool.List() {
				p.refreshPackages(st.UID)
			}
		}
	default:
		return fmt.Errorf("unknown task %q", taskID)
	}
	go func() {
		start := time.Now()
		log.Printf("[workbuddy] 手动执行任务 %s", taskID)
		fn()
		log.Printf("[workbuddy] 任务 %s 完成，用时 %s", taskID, time.Since(start).Round(time.Millisecond))
	}()
	return nil
}
