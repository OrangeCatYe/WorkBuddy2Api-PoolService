package scheduler

import (
	"testing"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/pool"
)

func TestListFor(t *testing.T) {
	p := pool.New("")
	for _, uid := range []string{"a", "b", "c"} {
		p.Add(&auth.Auth{UID: uid})
	}
	s := New(Config{Pool: p})
	if n := len(s.listFor(nil)); n != 3 {
		t.Fatalf("all = %d", n)
	}
	got := s.listFor([]string{"c", "a", "missing"})
	if len(got) != 2 {
		t.Fatalf("scoped = %+v", got)
	}
	seen := map[string]bool{}
	for _, st := range got {
		seen[st.UID] = true
	}
	if !seen["a"] || !seen["c"] || seen["b"] {
		t.Fatalf("scoped = %v", seen)
	}
	if n := len(s.listFor([]string{})); n != 0 {
		t.Fatalf("empty scope = %d", n)
	}
}
