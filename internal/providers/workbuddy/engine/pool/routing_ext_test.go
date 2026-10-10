package pool

import (
	"testing"
	"time"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
)

func TestRoutingBiasAndObserver(t *testing.T) {
	old := minPickGap
	minPickGap = 0
	defer func() { minPickGap = old }()

	p := New("")
	p.Add(&auth.Auth{UID: "a"})
	p.Add(&auth.Auth{UID: "b"})
	p.SetCredits("a", 100, 100)
	p.SetCredits("b", 100, 100)

	var events []PickEvent
	p.SetPickObserver(func(e PickEvent) { events = append(events, e) })
	// b 系数 0：只要 a 可用就永远不选 b
	p.SetRoutingBias(func(uid, model string) float64 {
		if uid == "b" && model == "paid" {
			return 0
		}
		return 1
	})
	for i := 0; i < 50; i++ {
		if got := p.PickExcludingForRealm(nil, "paid", ""); got == nil || got.UID != "a" {
			t.Fatalf("pick %d = %+v", i, got)
		}
	}
	if len(events) != 50 || events[0].Model != "paid" || len(events[0].Candidates) != 2 {
		t.Fatalf("events = %+v", events[0])
	}
	for _, c := range events[0].Candidates {
		if c.UID == "b" && (c.Bias != 0 || c.Weight != 0) {
			t.Fatalf("candidate b = %+v", c)
		}
	}
	// 其他模型不受影响：两个号都会被选到
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		seen[p.PickExcludingForRealm(nil, "free", "").UID] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("free model distribution = %v", seen)
	}

	// 全冷却兜底同样上报
	p.Cooldown("a", CoolSoft, time.Hour, "t")
	p.Cooldown("b", CoolSoft, 2*time.Hour, "t")
	events = nil
	if got := p.PickExcludingForRealm(nil, "paid", ""); got == nil || got.UID != "a" {
		t.Fatalf("fallback = %+v", got)
	}
	if len(events) != 1 || !events[0].Fallback {
		t.Fatalf("fallback event = %+v", events)
	}
}
