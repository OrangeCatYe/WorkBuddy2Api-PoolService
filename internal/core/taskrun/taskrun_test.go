package taskrun

import (
	"testing"
	"time"
)

func TestRecorderLifecycle(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Dir: dir})
	h := r.Begin(Spec{Provider: "wb", Task: "checkin", TaskName: "每日签到", Trigger: "manual", Scope: 2})
	i := h.Add(Item{AccountID: "a", Status: Running})
	h.Add(Item{AccountID: "b", Status: Skipped, Message: "今天已签到"})
	j := h.Add(Item{AccountID: "c"})
	if !r.Running("wb", "checkin") {
		t.Fatal("should be running")
	}
	if l := r.List(Filter{}); len(l) != 1 || l[0].Summary.Pending != 2 || l[0].Items != nil {
		t.Fatalf("live list = %+v", l)
	}
	h.Update(i, OK, "签到成功", 10, 2)
	_ = j // 不更新：结束时应被标为跳过
	h.End()
	h.End() // 幂等

	run, ok := r.Get(h.ID())
	if !ok || run.Status != RunOK || run.Summary.OK != 1 || run.Summary.Skipped != 2 || run.Summary.Credit != 10 || run.Items[2].Message != "未执行" {
		t.Fatalf("run = %+v", run)
	}
	if r.Running("wb", "checkin") {
		t.Fatal("should be ended")
	}

	h2 := r.Begin(Spec{Provider: "wb", Task: "travel"})
	h2.Add(Item{AccountID: "a", Status: OK})
	h2.Add(Item{AccountID: "b", Status: Failed})
	h2.End()
	if run, _ := r.Get(h2.ID()); run.Status != RunPartial {
		t.Fatalf("partial = %s", run.Status)
	}
	h3 := r.Begin(Spec{Provider: "wb", Task: "blackcat"})
	h3.Note("不在窗口")
	h3.End()
	latest := r.Latest()
	if latest["wb/blackcat"].Status != RunSkipped || latest["wb/checkin"].Summary.OK != 1 || len(latest) != 3 {
		t.Fatalf("latest = %+v", latest)
	}
	if l := r.List(Filter{Status: RunPartial}); len(l) != 1 || l[0].Task != "travel" {
		t.Fatalf("filter = %+v", l)
	}

	// 重启后回读
	r2 := New(Config{Dir: dir})
	if l := r2.List(Filter{}); len(l) != 3 || l[0].Task != "blackcat" {
		t.Fatalf("reloaded = %+v", l)
	}
	if run, ok := r2.Get(h.ID()); !ok || len(run.Items) != 3 {
		t.Fatalf("reloaded run = %+v", run)
	}

	// nil 安全
	var nr *Recorder
	nh := nr.Begin(Spec{})
	nh.Add(Item{})
	nh.Update(0, OK, "", 0, 0)
	nh.End()
}

func TestRetention(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Dir: dir, RetentionDays: 1})
	old := time.Now().AddDate(0, 0, -5)
	r.now = func() time.Time { return old }
	h := r.Begin(Spec{Provider: "wb", Task: "x"})
	h.End()
	r2 := New(Config{Dir: dir, RetentionDays: 1})
	if l := r2.List(Filter{}); len(l) != 0 {
		t.Fatalf("old run should be gc'd: %+v", l)
	}
}
