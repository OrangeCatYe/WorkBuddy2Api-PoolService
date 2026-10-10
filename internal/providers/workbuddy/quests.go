package workbuddy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/taskrun"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/growth"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/scheduler"
)

var (
	_ provider.QuestCapable     = (*Provider)(nil)
	_ provider.TaskHistoryAware = (*Provider)(nil)
)

// ErrTaskRunning 同一任务已有执行中的记录。
var ErrTaskRunning = errors.New("该任务正在执行，请等本轮结束")

// SetTaskRecorder 实现 provider.TaskHistoryAware：调度器的每次执行都写入记录。
func (p *Provider) SetTaskRecorder(r *taskrun.Recorder) {
	p.cfgMu.Lock()
	p.runs = r
	p.cfgMu.Unlock()
	if r == nil {
		p.sched.SetReporter(nil)
		return
	}
	p.sched.SetReporter(func(task, trigger string, scope int) scheduler.RunReporter {
		return p.beginRun(task, trigger, scope)
	})
}

func (p *Provider) recorder() *taskrun.Recorder {
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.runs
}

func (p *Provider) taskName(id string) string {
	for _, t := range p.Tasks() {
		if t.ID == id {
			return t.Name
		}
	}
	return id
}

// beginRun 开一条执行记录并包装为调度器 / 成长任务需要的写入端（nil 记录器时为空实现）。
func (p *Provider) beginRun(task, trigger string, scope int) *runAdapter {
	h := p.recorder().Begin(taskrun.Spec{Provider: ID, Task: task, TaskName: p.taskName(task), Trigger: trigger, Scope: scope})
	return &runAdapter{h: h}
}

// runAdapter 同时实现 scheduler.RunReporter 与 growth.Reporter。
type runAdapter struct {
	h  *taskrun.Handle
	mu sync.Mutex
}

func (r *runAdapter) Item(uid, nick, status, msg string, credit, energy float64) {
	r.h.Add(taskrun.Item{AccountID: uid, Account: nick, Status: status, Message: msg, Credit: credit, Energy: energy})
}

func (r *runAdapter) Add(uid, nick, code, title string) int {
	return r.h.Add(taskrun.Item{AccountID: uid, Account: nick, Code: code, Title: title})
}

func (r *runAdapter) Update(i int, status, msg string, credit, energy float64) {
	r.h.Update(i, status, msg, credit, energy)
}

func (r *runAdapter) Note(msg string) { r.h.Note(msg) }
func (r *runAdapter) End()            { r.h.End() }
func (r *runAdapter) ID() string      { return r.h.ID() }

// ---- 成长任务队列 ----

// runGrowthQueue 扫描并执行成长任务待办（同步）。定时触发与手动触发共用。
func (p *Provider) runGrowthQueue(only []string, trigger string) error {
	if p.growth.QueueRunning() {
		return growth.ErrRunning
	}
	rep := p.beginRun("growth", trigger, len(only))
	defer rep.End()
	err := p.growth.RunQueue(only, p.settings().Schedule.GrowthConcurrency, rep)
	if err != nil {
		rep.Note(err.Error())
	}
	return err
}

// ---- provider.QuestCapable ----

func questStatus(t growth.TaskView) string {
	switch {
	case t.Claimed:
		return provider.QuestClaimed
	case t.Locked:
		return provider.QuestLocked
	case t.Claimable || (t.Target > 0 && t.Current >= t.Target):
		return provider.QuestClaimable
	default:
		return provider.QuestTodo
	}
}

// Quests 实现 provider.QuestCapable。
func (p *Provider) Quests(_ context.Context, accountID string) ([]provider.Quest, error) {
	list, err := p.growth.List(accountID)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Quest, 0, len(list))
	for _, t := range list {
		desc := t.TaskDesc
		if desc == "" {
			desc = t.Description
		}
		tag := t.Tag
		if t.MP {
			tag = "小程序"
		}
		title := t.Title
		if title == "" {
			title = t.TaskCode
		}
		out = append(out, provider.Quest{
			Code: t.TaskCode, Title: title, Description: desc, Credit: t.Credit, Energy: t.Energy,
			Current: t.Current, Target: t.Target, Status: questStatus(t),
			Accepted: t.AcceptStatus != "" && t.AcceptStatus != "not_accepted", Tag: tag,
			Auto: t.Auto, AutoDesc: t.AutoDesc, Attempt: t.Attempt,
		})
	}
	return out, nil
}

// StartQuests 实现 provider.QuestCapable：异步执行并记录，codes 为空 = 该账号全部待办。
func (p *Provider) StartQuests(accountID string, codes []string) (string, error) {
	a := p.pool.AuthByUID(accountID)
	if a == nil {
		return "", fmt.Errorf("账号 %s 不存在", accountID)
	}
	if a.IsGlobal() || a.IsEnterprise() {
		return "", growth.ErrNoGrowth
	}
	if p.growth.Busy(accountID) {
		return "", growth.ErrBusy
	}
	if len(codes) == 0 {
		if p.growth.QueueRunning() {
			return "", growth.ErrRunning
		}
		rep := p.beginRun("growth", scheduler.TriggerManual, 1)
		go func() {
			defer rep.End()
			if err := p.growth.RunQueue([]string{accountID}, 1, rep); err != nil {
				rep.Note(err.Error())
			}
		}()
		return rep.ID(), nil
	}
	rep := p.beginRun("growth", scheduler.TriggerManual, 1)
	idx := make([]int, len(codes))
	for k, c := range codes {
		idx[k] = rep.Add(accountID, a.Nickname, c, c)
	}
	go func() {
		defer rep.End()
		for k, c := range codes {
			rep.Update(idx[k], taskrun.Running, "", 0, 0)
			res, err := p.growth.RunOne(accountID, c)
			switch {
			case errors.Is(err, growth.ErrNoTask):
				rep.Update(idx[k], taskrun.Skipped, err.Error(), 0, 0)
			case err != nil:
				rep.Update(idx[k], taskrun.Failed, err.Error(), 0, 0)
			default:
				msg := res.Message
				if res.ProgressAfter != "" {
					msg += "（进度 " + res.ProgressAfter + "）"
				}
				rep.Update(idx[k], res.Status, msg, float64(res.Credit), float64(res.Energy))
			}
		}
		log.Printf("[workbuddy] 账号 %s 手动执行成长任务 %v 完成", accountID, codes)
	}()
	return rep.ID(), nil
}

// ClaimQuest 实现 provider.QuestCapable。
func (p *Provider) ClaimQuest(_ context.Context, accountID, code string) (provider.QuestClaim, error) {
	c, e, already, err := p.growth.Claim(accountID, code)
	return provider.QuestClaim{Credit: c, Energy: e, Already: already}, err
}

// AcceptQuests 实现 provider.QuestCapable。
func (p *Provider) AcceptQuests(_ context.Context, accountID string) (int, error) {
	n, failed, err := p.growth.AcceptAll(accountID)
	if err == nil && len(failed) > 0 {
		err = fmt.Errorf("%d 个任务接受失败（上游拒绝），可重试", len(failed))
	}
	return n, err
}
