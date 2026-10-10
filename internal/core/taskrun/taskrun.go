// Package taskrun 记录定时 / 手动任务的每次执行：触发方式、逐账号结果、奖励。
//
// 运行中的记录可实时更新（面板轮询看进度），结束后按天追加写入 JSONL 文件，
// 重启时回读最近的记录。与平台无关：平台通过 Handle 上报，不感知存储细节。
package taskrun

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 条目状态。
const (
	Pending = "pending"
	Running = "running"
	OK      = "ok"
	Skipped = "skipped"
	Failed  = "error"
)

// 运行状态（Run.Status）。
const (
	RunRunning = "running"
	RunOK      = "ok"
	RunPartial = "partial" // 有成功也有失败
	RunFailed  = "failed"
	RunSkipped = "skipped" // 没有实际执行（全部跳过或没有可处理的账号）
)

// Item 单个账号（或账号上的一个子任务）的结果。
type Item struct {
	AccountID string     `json:"account_id"`
	Account   string     `json:"account,omitempty"` // 昵称
	Code      string     `json:"code,omitempty"`    // 子任务（如成长任务 code）
	Title     string     `json:"title,omitempty"`
	Status    string     `json:"status"`
	Message   string     `json:"message,omitempty"`
	Credit    float64    `json:"credit,omitempty"`
	Energy    float64    `json:"energy,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Summary 汇总。
type Summary struct {
	Total   int     `json:"total"`
	OK      int     `json:"ok"`
	Skipped int     `json:"skipped"`
	Failed  int     `json:"failed"`
	Pending int     `json:"pending"`
	Credit  float64 `json:"credit"`
	Energy  float64 `json:"energy"`
}

// Run 一次任务执行。
type Run struct {
	ID        string     `json:"id"`
	Provider  string     `json:"provider"`
	Task      string     `json:"task"`
	TaskName  string     `json:"task_name"`
	Trigger   string     `json:"trigger"` // schedule / manual
	Scope     int        `json:"scope"`   // 指定账号数，0 = 全部
	Status    string     `json:"status"`
	Note      string     `json:"note,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Summary   Summary    `json:"summary"`
	Items     []Item     `json:"items,omitempty"`
}

// Spec Begin 参数。
type Spec struct {
	Provider, Task, TaskName, Trigger string
	Scope                             int
}

// Config 存储配置。
type Config struct {
	Dir           string // 空 = 只在内存
	Capacity      int    // 内存保留的运行数，缺省 300
	RetentionDays int    // 缺省 30
}

// Recorder 执行记录存储。零值不可用，用 New 构造；nil Recorder 的 Begin 返回 nil Handle（全部方法空操作）。
type Recorder struct {
	cfg  Config
	mu   sync.Mutex
	runs []*Run // 按开始时间升序
	seq  int
	now  func() time.Time
}

// New 构造并回读归档。
func New(cfg Config) *Recorder {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 300
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 30
	}
	r := &Recorder{cfg: cfg, now: time.Now}
	if cfg.Dir != "" {
		_ = os.MkdirAll(cfg.Dir, 0o755)
		r.gc()
		r.preload()
	}
	return r
}

// Begin 开始一次执行。
func (r *Recorder) Begin(s Spec) *Handle {
	if r == nil {
		return nil
	}
	now := r.now()
	r.mu.Lock()
	r.seq++
	run := &Run{
		ID:       fmt.Sprintf("%s-%04d", now.Format("20060102-150405"), r.seq%10000),
		Provider: s.Provider, Task: s.Task, TaskName: s.TaskName, Trigger: s.Trigger, Scope: s.Scope,
		Status: RunRunning, StartedAt: now,
	}
	r.runs = append(r.runs, run)
	if len(r.runs) > r.cfg.Capacity {
		// 只淘汰已结束的最早记录，运行中的不淘汰
		for i, old := range r.runs {
			if old.EndedAt != nil {
				r.runs = append(r.runs[:i], r.runs[i+1:]...)
				break
			}
		}
	}
	r.mu.Unlock()
	return &Handle{r: r, run: run}
}

// Handle 一次执行的写入句柄，并发安全；nil Handle 的方法均为空操作。
type Handle struct {
	r    *Recorder
	run  *Run
	once sync.Once
}

// ID 运行 ID。
func (h *Handle) ID() string {
	if h == nil {
		return ""
	}
	return h.run.ID
}

// Add 追加条目，返回下标。Status 为空时视为 pending。
func (h *Handle) Add(it Item) int {
	if h == nil {
		return -1
	}
	if it.Status == "" {
		it.Status = Pending
	}
	now := h.r.now()
	if it.Status != Pending && it.Status != Running {
		it.EndedAt = &now
	}
	if it.Status == Running {
		it.StartedAt = &now
	}
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	h.run.Items = append(h.run.Items, it)
	return len(h.run.Items) - 1
}

// Update 更新条目状态；credit/energy 为本条累计奖励（覆盖写）。
func (h *Handle) Update(i int, status, msg string, credit, energy float64) {
	if h == nil {
		return
	}
	now := h.r.now()
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	if i < 0 || i >= len(h.run.Items) {
		return
	}
	it := &h.run.Items[i]
	it.Status, it.Message = status, msg
	if credit != 0 {
		it.Credit = credit
	}
	if energy != 0 {
		it.Energy = energy
	}
	switch status {
	case Running:
		it.StartedAt = &now
	case Pending:
	default:
		it.EndedAt = &now
	}
}

// Note 设置运行级说明（如「不在夜间窗口，未执行」）。
func (h *Handle) Note(s string) {
	if h == nil {
		return
	}
	h.r.mu.Lock()
	h.run.Note = s
	h.r.mu.Unlock()
}

// End 结束：计算状态并归档。多次调用只生效一次。
func (h *Handle) End() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		now := h.r.now()
		h.r.mu.Lock()
		run := h.run
		for i := range run.Items {
			// 结束时仍未执行的条目视为跳过（如队列被中断）
			if run.Items[i].Status == Pending || run.Items[i].Status == Running {
				run.Items[i].Status = Skipped
				if run.Items[i].Message == "" {
					run.Items[i].Message = "未执行"
				}
			}
		}
		run.EndedAt = &now
		run.Summary = summarize(run.Items)
		run.Status = statusOf(run.Summary)
		line, _ := json.Marshal(run)
		h.r.mu.Unlock()
		h.r.archive(run.StartedAt, line)
	})
}

func summarize(items []Item) Summary {
	s := Summary{Total: len(items)}
	for _, it := range items {
		switch it.Status {
		case OK:
			s.OK++
		case Skipped:
			s.Skipped++
		case Failed:
			s.Failed++
		default:
			s.Pending++
		}
		s.Credit += it.Credit
		s.Energy += it.Energy
	}
	return s
}

func statusOf(s Summary) string {
	switch {
	case s.Failed > 0 && s.OK == 0:
		return RunFailed
	case s.Failed > 0:
		return RunPartial
	case s.OK > 0:
		return RunOK
	default:
		return RunSkipped
	}
}

// ---- 查询 ----

// Filter 列表筛选。
type Filter struct {
	Provider, Task, Status string
	Limit                  int
}

// List 按开始时间倒序返回运行摘要（不含 Items）。运行中的记录实时计算汇总。
func (r *Recorder) List(f Filter) []Run {
	if r == nil {
		return nil
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Run, 0, f.Limit)
	for i := len(r.runs) - 1; i >= 0 && len(out) < f.Limit; i-- {
		run := r.runs[i]
		if (f.Provider != "" && run.Provider != f.Provider) || (f.Task != "" && run.Task != f.Task) || (f.Status != "" && run.Status != f.Status) {
			continue
		}
		c := *run
		if c.EndedAt == nil {
			c.Summary = summarize(run.Items)
		}
		c.Items = nil
		out = append(out, c)
	}
	return out
}

// Get 取单次运行（含 Items 副本）。
func (r *Recorder) Get(id string) (Run, bool) {
	if r == nil {
		return Run{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.runs) - 1; i >= 0; i-- {
		if r.runs[i].ID == id {
			c := *r.runs[i]
			c.Items = append([]Item(nil), r.runs[i].Items...)
			if c.EndedAt == nil {
				c.Summary = summarize(c.Items)
			}
			return c, true
		}
	}
	return Run{}, false
}

// Latest 每个 provider/task 最近一次运行的摘要（键 = provider/task）。
func (r *Recorder) Latest() map[string]Run {
	out := map[string]Run{}
	if r == nil {
		return out
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.runs) - 1; i >= 0; i-- {
		run := r.runs[i]
		k := run.Provider + "/" + run.Task
		if _, ok := out[k]; ok {
			continue
		}
		c := *run
		if c.EndedAt == nil {
			c.Summary = summarize(run.Items)
		}
		c.Items = nil
		out[k] = c
	}
	return out
}

// Running 指定 provider/task 是否有运行中的记录。
func (r *Recorder) Running(provider, task string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.runs {
		if run.EndedAt == nil && run.Provider == provider && run.Task == task {
			return true
		}
	}
	return false
}

// ---- 归档 ----

func (r *Recorder) dayFile(day string) string { return filepath.Join(r.cfg.Dir, "runs-"+day+".jsonl") }

func (r *Recorder) archive(started time.Time, line []byte) {
	if r.cfg.Dir == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := os.OpenFile(r.dayFile(started.Format("2006-01-02")), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

func (r *Recorder) preload() {
	files, _ := filepath.Glob(filepath.Join(r.cfg.Dir, "runs-*.jsonl"))
	sort.Strings(files)
	cutoff := r.now().AddDate(0, 0, -r.cfg.RetentionDays)
	var runs []*Run
	for i := len(files) - 1; i >= 0 && len(runs) < r.cfg.Capacity; i-- {
		f, err := os.Open(files[i])
		if err != nil {
			continue
		}
		var day []*Run
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for sc.Scan() {
			var run Run
			if json.Unmarshal(sc.Bytes(), &run) == nil && run.ID != "" && run.StartedAt.After(cutoff) {
				day = append(day, &run)
			}
		}
		_ = f.Close()
		runs = append(day, runs...)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.Before(runs[j].StartedAt) })
	if len(runs) > r.cfg.Capacity {
		runs = runs[len(runs)-r.cfg.Capacity:]
	}
	r.runs = runs
}

func (r *Recorder) gc() {
	cutoff := r.now().AddDate(0, 0, -r.cfg.RetentionDays).Format("2006-01-02")
	files, _ := filepath.Glob(filepath.Join(r.cfg.Dir, "runs-*.jsonl"))
	for _, f := range files {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "runs-"), ".jsonl")
		if day < cutoff {
			_ = os.Remove(f)
		}
	}
}
