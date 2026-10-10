package growth

import (
	"testing"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/upstream"
)

// 同一账号的任务锁互斥，不同账号互不影响（自原项目 autotask_lock_test 迁移）。
func TestAccountLock(t *testing.T) {
	r := New(nil, nil)
	if !r.tryLock("u1") {
		t.Fatal("首次加锁应成功")
	}
	if r.tryLock("u1") || !r.Busy("u1") {
		t.Fatal("同账号第二次加锁应失败")
	}
	if !r.tryLock("u2") {
		t.Fatal("不同账号不应互斥")
	}
	r.unlock("u2")
	r.unlock("u1")
	if r.Busy("u1") {
		t.Fatal("解锁后应空闲")
	}
}

func TestActionsCatalog(t *testing.T) {
	acts := Actions()
	if len(acts) < 17 {
		t.Fatalf("actions = %d, want >= 17", len(acts))
	}
	seen := map[string]bool{}
	for _, a := range acts {
		if a.Code == "" || a.Desc == "" || seen[a.Code] {
			t.Fatalf("bad action %+v", a)
		}
		seen[a.Code] = true
	}
	// 依赖顺序：chat_5 必须排在 first_buddy 之前（领养依赖活跃上报）
	if autoActionIndex("chat_5") > autoActionIndex("first_buddy") {
		t.Fatal("chat_5 must run before first_buddy")
	}
	if !Actions()[autoActionIndex("school_season")].MP {
		t.Fatal("school_season should be mp")
	}
}

func TestGrowthPending(t *testing.T) {
	cases := []struct {
		t    upstream.Task
		want bool
	}{
		{upstream.Task{TaskCode: "chat_5", Target: 5, Current: 2}, true},
		{upstream.Task{TaskCode: "chat_5", Target: 5, Current: 5}, true}, // 达标未领也入队
		{upstream.Task{TaskCode: "chat_5", Claimed: true}, false},
		{upstream.Task{TaskCode: "chat_5", Locked: true}, false},
		{upstream.Task{TaskCode: "Expert_Philanthropy"}, false}, // 无自动动作
	}
	for _, c := range cases {
		if got := growthPending(c.t); got != c.want {
			t.Errorf("%+v: got %v", c.t, got)
		}
	}
}
