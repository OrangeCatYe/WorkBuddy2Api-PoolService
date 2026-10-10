// Package growth WorkBuddy 成长任务（积分任务）的查询、接受、领奖与自动完成。
//
// 动作实现（actions.go）自原项目 internal/panel/autotask.go 迁移、逻辑未改；
// 本文件替代原项目 Panel 上的状态（账号锁、队列）与 HTTP 处理函数，
// 改为返回数据的方法，由 Provider 适配到通用能力接口。
package growth

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/pool"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/upstream"
)

// 错误。
var (
	ErrBusy     = errors.New("该账号有任务动作正在执行，请等本轮结束后再试")
	ErrNoAction = errors.New("该任务需要在官方客户端内操作，无法自动完成")
	ErrNoTask   = errors.New("该账号没有此任务")
	ErrNoAcct   = errors.New("账号不存在")
	ErrRunning  = errors.New("成长任务队列正在执行")
	ErrNoGrowth = errors.New("国际版和企业版账号没有成长任务")
)

// acceptBatchGap 批量接受的批间节流（对齐脚本 1.05s 口径，避免上游风控）。
var acceptBatchGap = 1050 * time.Millisecond

// Runner 成长任务执行器。
type Runner struct {
	pool *pool.Pool
	up   *upstream.Client

	lockMu sync.Mutex
	locks  map[string]*sync.Mutex

	queueMu sync.Mutex
	running bool
}

// New 构造。
func New(p *pool.Pool, up *upstream.Client) *Runner {
	return &Runner{pool: p, up: up, locks: map[string]*sync.Mutex{}}
}

// tryLock 同一账号的任务动作同时只允许一条在跑（不同账号之间不互斥）。
func (r *Runner) tryLock(uid string) bool {
	r.lockMu.Lock()
	mu := r.locks[uid]
	if mu == nil {
		mu = &sync.Mutex{}
		r.locks[uid] = mu
	}
	r.lockMu.Unlock()
	return mu.TryLock()
}

func (r *Runner) unlock(uid string) {
	r.lockMu.Lock()
	mu := r.locks[uid]
	r.lockMu.Unlock()
	if mu != nil {
		mu.Unlock()
	}
}

// Busy 账号是否有任务动作在执行。
func (r *Runner) Busy(uid string) bool {
	if !r.tryLock(uid) {
		return true
	}
	r.unlock(uid)
	return false
}

// QueueRunning 队列是否在执行。
func (r *Runner) QueueRunning() bool {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	return r.running
}

func (r *Runner) account(uid string) (*auth.Auth, error) {
	a := r.pool.AuthByUID(uid)
	if a == nil {
		return nil, ErrNoAcct
	}
	if a.IsGlobal() || a.IsEnterprise() {
		return a, ErrNoGrowth
	}
	return a, nil
}

// ---- 目录 ----

// ActionInfo 可自动完成的任务。
type ActionInfo struct {
	Code    string `json:"code"`
	Desc    string `json:"desc"`
	Attempt bool   `json:"attempt,omitempty"` // 尝试型：上游未证实可脚本化
	MP      bool   `json:"mp,omitempty"`      // 小程序口径
}

// Actions 全部可自动完成的任务（顺序即队列执行顺序）。
func Actions() []ActionInfo {
	out := make([]ActionInfo, len(autoActions))
	for i, a := range autoActions {
		out[i] = ActionInfo{Code: a.TaskCode, Desc: a.Desc, Attempt: a.Attempt, MP: isMPTaskCode(a.TaskCode)}
	}
	return out
}

// TaskView 任务列表条目（上游任务 + 自动化信息）。
type TaskView struct {
	upstream.Task
	Auto     bool   `json:"auto"`
	AutoDesc string `json:"auto_desc,omitempty"`
	Attempt  bool   `json:"attempt,omitempty"`
	MP       bool   `json:"mp,omitempty"`
	Pending  bool   `json:"pending"` // 未完成且可自动化（会进入队列）
}

// listMerged 默认口径 + 小程序口径（按 code 去重；mp 失败静默）。
func (r *Runner) listMerged(a *auth.Auth) ([]upstream.Task, error) {
	tasks, err := r.up.ListTasks(a)
	if err != nil {
		return nil, err
	}
	if mpTasks, mpErr := r.up.ListTasksMP(a); mpErr == nil {
		seen := map[string]bool{}
		for _, t := range tasks {
			seen[t.TaskCode] = true
		}
		for _, t := range mpTasks {
			if !seen[t.TaskCode] {
				tasks = append(tasks, t)
				seen[t.TaskCode] = true
			}
		}
	}
	return tasks, nil
}

// List 单账号全部任务。
func (r *Runner) List(uid string) ([]TaskView, error) {
	a, err := r.account(uid)
	if err != nil {
		return nil, err
	}
	tasks, err := r.listMerged(a)
	if err != nil {
		return nil, err
	}
	out := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		v := TaskView{Task: t, Pending: growthPending(t), MP: isMPTaskCode(t.TaskCode)}
		if act := autoActionFor(t.TaskCode); act != nil {
			v.Auto, v.AutoDesc, v.Attempt = true, act.Desc, act.Attempt
		}
		out = append(out, v)
	}
	return out, nil
}

// growthPending 任务是否"未完成且可自动化"。
func growthPending(t upstream.Task) bool {
	if t.Claimed {
		return false
	}
	// 上游锁定的任务不出待办：Sequential 族每日零点解锁一环，刚做完上一环时
	// 下一环以 locked 形态出现在列表里——扫进队列只会 accept 不落账报失败。
	if t.Locked {
		return false
	}
	// 达标未领也入队（执行后会自动领），但仅限有自动化动作的任务。
	return autoActionFor(t.TaskCode) != nil
}

// ---- 接受 / 领奖 ----

// AcceptAll 接受该账号全部尚未接受的任务（跳过已接受 / 已领取 / 锁定的），返回接受数。
func (r *Runner) AcceptAll(uid string) (accepted int, failed []string, err error) {
	a, err := r.account(uid)
	if err != nil {
		return 0, nil, err
	}
	tasks, err := r.up.ListTasks(a)
	if err != nil {
		return 0, nil, err
	}
	var codes []string
	for _, t := range tasks {
		if t.Claimed || t.Locked || t.AcceptStatus == "accepted" || t.AcceptStatus == "completed" {
			continue
		}
		codes = append(codes, t.TaskCode)
	}
	const batch = 20
	for i := 0; i < len(codes); i += batch {
		end := min(i+batch, len(codes))
		if err := r.up.AcceptTasks(a, codes[i:end]); err != nil {
			log.Printf("growth: 批量接受失败 uid=%s codes=%v err=%v", uid, codes[i:end], err)
			failed = append(failed, codes[i:end]...)
			continue
		}
		accepted += end - i
		time.Sleep(acceptBatchGap)
	}
	// 小程序口径任务单独批量接受（默认列表不含 mp 码，accept 也要求 mp 头）。
	if mpTasks, mpErr := r.up.ListTasksMP(a); mpErr == nil {
		var mpCodes []string
		for _, t := range mpTasks {
			if t.Claimed || t.Locked || t.AcceptStatus == "accepted" || t.AcceptStatus == "completed" {
				continue
			}
			mpCodes = append(mpCodes, t.TaskCode)
		}
		if len(mpCodes) > 0 {
			if err := r.up.AcceptTasksMP(a, mpCodes); err != nil {
				log.Printf("growth: mp 批量接受失败 uid=%s err=%v", uid, err)
				failed = append(failed, mpCodes...)
			} else {
				accepted += len(mpCodes)
				time.Sleep(acceptBatchGap)
			}
		}
	}
	log.Printf("growth: 全部接受 uid=%s 接受=%d 失败=%d", uid, accepted, len(failed))
	return accepted, failed, nil
}

// Claim 领取奖励；already=true 表示此前已领取。
func (r *Runner) Claim(uid, code string) (credit, energy int64, already bool, err error) {
	a, err := r.account(uid)
	if err != nil {
		return 0, 0, false, err
	}
	credit, energy, err = r.claim(a, code)
	if err != nil {
		return 0, 0, false, err
	}
	log.Printf("growth: 领取任务奖励 uid=%s code=%s +%d分 +%d能", uid, code, credit, energy)
	return credit, energy, credit == 0 && energy == 0, nil
}

// claim 小程序口径任务走 chat 域 mp 头领奖（缺头实测不可领）；其余 Web 端接口。
func (r *Runner) claim(a *auth.Auth, code string) (int64, int64, error) {
	if isMPTaskCode(code) {
		return r.up.ClaimRewardMP(a, code)
	}
	return r.up.ClaimReward(a, code)
}

// ---- 执行 ----

// Result 单个任务的执行结果。
type Result struct {
	Code           string `json:"code"`
	Status         string `json:"status"` // ok / skipped / error
	Message        string `json:"message"`
	ProgressBefore string `json:"progress_before,omitempty"`
	ProgressAfter  string `json:"progress_after,omitempty"`
	Claimed        bool   `json:"claimed,omitempty"`
	Credit         int64  `json:"credit,omitempty"`
	Energy         int64  `json:"energy,omitempty"`
}

// RunOne 自动完成单个任务：动作 → 有界轮询回读 → 达标即领奖。账号已有动作在执行时返回 ErrBusy。
func (r *Runner) RunOne(uid, code string) (Result, error) {
	a, err := r.account(uid)
	if err != nil {
		return Result{}, err
	}
	if autoActionFor(code) == nil {
		return Result{}, ErrNoAction
	}
	if !r.tryLock(uid) {
		return Result{}, ErrBusy
	}
	defer r.unlock(uid)
	return r.runOne(a, code)
}

// runOne 调用方已持有账号锁。
func (r *Runner) runOne(a *auth.Auth, code string) (Result, error) {
	res := Result{Code: code}
	act := autoActionFor(code)
	if act == nil {
		return res, ErrNoAction
	}
	// 前置读取：taskByCode 已双口径（mp 专属码自动回落 mp 列表）。
	before, err := r.taskByCode(a, code)
	if err != nil {
		return res, err
	}
	if before == nil {
		return res, ErrNoTask
	}
	res.ProgressBefore = taskProgressText(before)
	if before.Claimed {
		res.Status, res.Message = "skipped", "已完成（已领取）"
		return res, nil
	}
	msg, err := act.run(r, a)
	if err != nil {
		return res, err
	}
	// 回读验证：上报 200 ≠ 计分（上游可能静默丢弃 + 计分异步），有界轮询后再决定是否领奖。
	isMP := isMPTaskCode(code)
	var after *upstream.Task
	if isMP {
		after, _ = r.taskByCodeMP(a, code)
	} else {
		after, _ = r.taskByCodeWaiting(a, code)
	}
	res.Status, res.Message = "ok", msg
	if after != nil {
		res.ProgressAfter = taskProgressText(after)
		if after.Claimed {
			res.Claimed = true
		}
		if after.Claimable {
			credit, energy, cerr := r.claim(a, code)
			switch {
			case cerr != nil:
				res.Message += "；达标但领奖失败，可稍后手动领取"
			case credit > 0 || energy > 0:
				res.Claimed, res.Credit, res.Energy = true, credit, energy
				res.Message += fmt.Sprintf("；已自动领奖 +%d 积分 +%d 能量", credit, energy)
			default:
				res.Claimed = true
				res.Message += "；奖励此前已领取"
			}
		}
	}
	log.Printf("growth: 任务动作 uid=%s code=%s progress %s -> %s claimed=%v", a.UID, code, res.ProgressBefore, res.ProgressAfter, res.Claimed)
	return res, nil
}

// Reporter 队列执行的结果写入端。
type Reporter interface {
	Add(uid, nickname, code, title string) int
	Update(i int, status, msg string, credit, energy float64)
	Note(msg string)
}

type queueAccount struct {
	a    *auth.Auth
	grow []upstream.Task
	idx  []int
}

// RunQueue 扫描账号待办并执行（only 为 nil = 全部未禁用账号）：账号内串行（与单任务共用账号锁），
// 账号间按 concurrency（1-4）并发。同步执行，返回时队列已结束。队列已在执行时返回 ErrRunning。
func (r *Runner) RunQueue(only []string, concurrency int, rep Reporter) error {
	concurrency = max(1, min(4, concurrency))
	r.queueMu.Lock()
	if r.running {
		r.queueMu.Unlock()
		return ErrRunning
	}
	r.running = true
	r.queueMu.Unlock()
	defer func() {
		r.queueMu.Lock()
		r.running = false
		r.queueMu.Unlock()
	}()

	var set map[string]bool
	if only != nil {
		set = map[string]bool{}
		for _, u := range only {
			set[u] = true
		}
	}
	var (
		accts []*queueAccount
		mu    sync.Mutex
		wg    sync.WaitGroup
	)
	for _, st := range r.pool.List() {
		if set != nil && !set[st.UID] {
			continue
		}
		if set == nil && st.Disabled {
			continue
		}
		a := r.pool.AuthByUID(st.UID)
		if a == nil || a.IsGlobal() || a.IsEnterprise() {
			continue // 国际版 / 企业版无成长体系，不发起任何上游调用
		}
		wg.Add(1)
		go func(a *auth.Auth) {
			defer wg.Done()
			tasks, err := r.listMerged(a)
			if err != nil {
				mu.Lock()
				i := rep.Add(a.UID, a.Nickname, "", "查询任务")
				mu.Unlock()
				rep.Update(i, "error", "查询任务失败："+err.Error(), 0, 0)
				return
			}
			one := &queueAccount{a: a}
			for _, t := range tasks {
				if growthPending(t) {
					one.grow = append(one.grow, t)
				}
			}
			sort.SliceStable(one.grow, func(i, j int) bool { // 按 autoActions 顺序（依赖前置）
				return autoActionIndex(one.grow[i].TaskCode) < autoActionIndex(one.grow[j].TaskCode)
			})
			if len(one.grow) > 0 {
				mu.Lock()
				accts = append(accts, one)
				mu.Unlock()
			}
		}(a)
	}
	wg.Wait()

	// 登记全部条目（pending），面板可看到完整队列与进度。
	sort.SliceStable(accts, func(i, j int) bool { return accts[i].a.UID < accts[j].a.UID })
	total := 0
	for _, one := range accts {
		for _, t := range one.grow {
			title := t.Title
			if title == "" {
				title = t.TaskCode
			}
			one.idx = append(one.idx, rep.Add(one.a.UID, one.a.Nickname, t.TaskCode, title))
			total++
		}
	}
	if total == 0 {
		rep.Note("没有待完成的成长任务")
		log.Printf("growth: 队列启动：无可执行待办")
		return nil
	}
	log.Printf("growth: 队列启动：%d 个账号 %d 项（并发 %d）", len(accts), total, concurrency)

	sem := make(chan struct{}, concurrency)
	for _, one := range accts {
		wg.Add(1)
		go func(one *queueAccount) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if !r.tryLock(one.a.UID) {
				for _, i := range one.idx {
					rep.Update(i, "skipped", "该账号有其它任务动作在执行，跳过", 0, 0)
				}
				return
			}
			defer r.unlock(one.a.UID)
			// 前置：批量接受尚未接受的任务（上游对 not_accepted 的任务不计数）。失败不阻塞。
			if r.acceptPending(one.a) > 0 {
				time.Sleep(reportGap)
			}
			for k, t := range one.grow {
				i := one.idx[k]
				rep.Update(i, "running", "", 0, 0)
				res, err := r.runOne(one.a, t.TaskCode)
				switch {
				case errors.Is(err, ErrNoTask):
					rep.Update(i, "skipped", "该账号无此任务", 0, 0)
				case err != nil:
					rep.Update(i, "error", err.Error(), 0, 0)
				default:
					msg := res.Message
					if res.ProgressAfter != "" {
						msg += "（进度 " + res.ProgressAfter + "）"
					}
					rep.Update(i, res.Status, msg, float64(res.Credit), float64(res.Energy))
				}
				time.Sleep(reportGap) // 项间节流
			}
		}(one)
	}
	wg.Wait()
	log.Printf("growth: 队列执行结束（共 %d 项）", total)
	return nil
}

// acceptPending 批量接受该账号未接受的任务（默认 + 小程序口径），返回接受的个数（失败返回 0 不阻塞）。
func (r *Runner) acceptPending(a *auth.Auth) int {
	n := 0
	if tasks, err := r.up.ListTasks(a); err == nil {
		var codes []string
		for _, t := range tasks {
			if !t.Claimed && !t.Locked && t.AcceptStatus != "accepted" && t.AcceptStatus != "completed" {
				codes = append(codes, t.TaskCode)
			}
		}
		if len(codes) > 0 {
			if err := r.up.AcceptTasks(a, codes); err != nil {
				log.Printf("growth: 队列 accept uid=%s: %v（不阻塞）", a.UID, err)
			} else {
				n += len(codes)
			}
		}
	}
	if mpTasks, err := r.up.ListTasksMP(a); err == nil {
		var codes []string
		for _, t := range mpTasks {
			if !t.Claimed && !t.Locked && t.AcceptStatus != "accepted" && t.AcceptStatus != "completed" {
				codes = append(codes, t.TaskCode)
			}
		}
		if len(codes) > 0 && r.up.AcceptTasksMP(a, codes) == nil {
			n += len(codes)
		}
	}
	if n > 0 {
		log.Printf("growth: 队列 accept uid=%s: 已接受 %d 个任务", a.UID, n)
	}
	return n
}

// ---- 开学季券码 ----

// VoucherRow 单账号券码。
type VoucherRow struct {
	UID      string                   `json:"uid"`
	Nickname string                   `json:"nickname"`
	Vouchers []upstream.SchoolVoucher `json:"vouchers"`
	Err      string                   `json:"error,omitempty"`
}

// Vouchers 逐 CN 账号查开学季券码（3 并发）。
func (r *Runner) Vouchers() []VoucherRow {
	states := r.pool.List()
	out := make([]VoucherRow, len(states))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, st := range states {
		a := r.pool.AuthByUID(st.UID)
		if st.Disabled || a == nil || a.IsGlobal() || a.IsEnterprise() {
			continue
		}
		wg.Add(1)
		go func(i int, a *auth.Auth) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			row := VoucherRow{UID: a.UID, Nickname: a.Nickname}
			if vs, err := r.up.SchoolVouchers(a); err != nil {
				row.Err = err.Error()
			} else {
				row.Vouchers = vs
			}
			out[i] = row
		}(i, a)
	}
	wg.Wait()
	res := make([]VoucherRow, 0, len(out))
	for _, row := range out {
		if row.UID != "" {
			res = append(res, row)
		}
	}
	return res
}
