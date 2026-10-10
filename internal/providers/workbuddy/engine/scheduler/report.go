package scheduler

import "fmt"

// 触发方式。
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// 条目状态（与 core/taskrun 同值）。
const (
	StatusOK      = "ok"
	StatusSkipped = "skipped"
	StatusFailed  = "error"
)

// RunReporter 一次任务执行的结果写入端。
type RunReporter interface {
	Item(uid, nickname, status, msg string, credit, energy float64)
	Note(msg string)
	End()
}

// Reporter 开始一次执行记录。task 为任务 ID（checkin / travel …），scope 为指定账号数（0 = 全部）。
type Reporter func(task, trigger string, scope int) RunReporter

type nopReporter struct{}

func (nopReporter) Item(string, string, string, string, float64, float64) {}
func (nopReporter) Note(string)                                           {}
func (nopReporter) End()                                                  {}

// SetReporter 挂载执行记录（nil 关闭）。
func (s *Scheduler) SetReporter(fn Reporter) {
	s.schedMu.Lock()
	s.reporter = fn
	s.schedMu.Unlock()
}

// begin 开始记录；未挂载时返回空实现。定时余额刷新频率高、无奖励，不记录。
func (s *Scheduler) begin(task string, only []string, trigger string) RunReporter {
	s.schedMu.Lock()
	fn := s.reporter
	s.schedMu.Unlock()
	if fn == nil {
		return nopReporter{}
	}
	if r := fn(task, trigger, len(only)); r != nil {
		return r
	}
	return nopReporter{}
}

func errMsg(prefix string, err error) string { return fmt.Sprintf("%s：%v", prefix, err) }
