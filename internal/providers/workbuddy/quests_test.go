package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/orangeruan/poolservice/internal/core/taskrun"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/growth"
)

func TestTaskHistory(t *testing.T) {
	s, err := ParseSettings(json.RawMessage(`{"schedule":{"balance_refresh_enabled":false}}`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	defer p.pool.Close()
	rec := taskrun.New(taskrun.Config{})
	p.SetTaskRecorder(rec)

	// 空池：每个任务都会留下一条「跳过」的记录，名称取自任务清单
	for _, id := range []string{"checkin", "keepalive", "packages", "growth"} {
		if err := p.RunTask(context.Background(), id, nil); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		latest := rec.Latest()
		done := 0
		for _, r := range latest {
			if r.EndedAt != nil {
				done++
			}
		}
		// checkin 结束后会接着跑连登奖励，共 5 条
		if done >= 5 {
			if r := latest[ID+"/checkin"]; r.TaskName != "每日签到" || r.Trigger != "manual" || r.Status != taskrun.RunSkipped {
				t.Fatalf("checkin run = %+v", r)
			}
			if _, ok := latest[ID+"/streak"]; !ok {
				t.Fatal("签到后应执行连登奖励")
			}
			if r := latest[ID+"/growth"]; r.Note == "" {
				t.Fatalf("growth run should note empty queue: %+v", r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runs not finished: %+v", latest)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 同一任务正在执行时拒绝重复触发
	h := rec.Begin(taskrun.Spec{Provider: ID, Task: "travel"})
	if err := p.RunTask(context.Background(), "travel", nil); !errors.Is(err, ErrTaskRunning) {
		t.Fatalf("want ErrTaskRunning, got %v", err)
	}
	h.End()

	if err := p.RunTask(context.Background(), "checkin", []string{"nope"}); err == nil {
		t.Fatal("unknown account should fail")
	}
	if _, err := p.StartQuests("nope", nil); err == nil {
		t.Fatal("unknown account should fail")
	}
	if _, err := p.Quests(context.Background(), "nope"); !errors.Is(err, growth.ErrNoAcct) {
		t.Fatalf("quests err = %v", err)
	}
}
