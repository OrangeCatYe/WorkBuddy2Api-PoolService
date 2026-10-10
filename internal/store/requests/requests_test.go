package requests

import (
	"testing"
	"time"
)

func TestRecorderQueryStatsAndReload(t *testing.T) {
	dir := t.TempDir()
	r := New(Config{Dir: dir, Capacity: 3})
	now := time.Now()
	for i, e := range []Entry{
		{ID: "a", Model: "glm-5.2", Provider: "workbuddy", OK: true, Credit: 1, PromptTokens: 10, TTFBMs: 100},
		{ID: "b", Model: "hy3", Provider: "workbuddy", OK: false, Error: "boom"},
		{ID: "c", Model: "glm-5.2", Provider: "workbuddy", OK: true, Credit: 2, TTFBMs: 300},
		{ID: "d", Model: "glm-5.2", Provider: "workbuddy", OK: true, Credit: 3, TTFBMs: 200},
	} {
		e.Time = now.Add(time.Duration(i) * time.Second)
		r.Add(e)
	}
	all := r.Query(Filter{})
	if len(all) != 3 || all[0].ID != "d" || all[2].ID != "b" {
		t.Fatalf("ring = %+v", all)
	}
	if got := r.Query(Filter{Status: "error"}); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("error filter = %+v", got)
	}
	if got := r.Query(Filter{Q: "BOOM"}); len(got) != 1 {
		t.Fatalf("search = %+v", got)
	}
	st := r.Stats(24)
	if st.Total.Requests != 3 || st.Total.Credit != 5 || st.ByModel[0].Key != "glm-5.2" || st.ByModel[0].AvgTTFBMs != 250 {
		t.Fatalf("stats = %+v", st)
	}
	r.Close()

	r2 := New(Config{Dir: dir, Capacity: 10})
	if got := r2.Query(Filter{}); len(got) != 4 {
		t.Fatalf("reload = %d", len(got))
	}
	r2.Close()
}
