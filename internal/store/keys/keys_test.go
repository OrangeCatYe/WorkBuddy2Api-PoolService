package keys

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLifecycle(t *testing.T) {
	file := filepath.Join(t.TempDir(), "keys.json")
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Spec{Name: " "}, ""); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := s.Create(Spec{Name: "x", Models: []string{"[bad"}}, ""); err == nil {
		t.Fatal("bad pattern must fail")
	}
	k, err := s.Create(Spec{Name: "dev", Models: []string{"glm-*", "workbuddy/hy3"}, DailyCreditBudget: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !k.AllowsModel("GLM-5.2") || !k.AllowsModel("hy3", "workbuddy/hy3") || k.AllowsModel("kimi-k3", "workbuddy/kimi-k3") {
		t.Fatal("whitelist mismatch")
	}

	day := time.Date(2026, 10, 10, 23, 0, 0, 0, time.Local)
	s.now = func() time.Time { return day }
	s.Record(k.ID, true, 10, 5, 2, 12)
	if _, d := s.Authenticate(k.Secret); d == nil || d.Status != 429 || d.RetryAfter != time.Hour {
		t.Fatalf("daily budget: %+v", d)
	}
	s.now = func() time.Time { return day.Add(2 * time.Hour) } // 次日
	if _, d := s.Authenticate(k.Secret); d != nil {
		t.Fatalf("budget must reset next day: %+v", d)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	// 重新加载：用量与设置保留
	s2, _ := Open(file)
	got, ok := s2.Get(k.ID)
	if !ok || got.Usage.Credit != 12 || got.Usage.PromptTokens != 10 || len(got.Models) != 2 {
		t.Fatalf("reload = %+v", got)
	}
	old := got.Secret
	r, _ := s2.Rotate(k.ID)
	if r.Secret == old {
		t.Fatal("rotate kept secret")
	}
	if _, d := s2.Authenticate(old); d == nil || d.Status != 401 {
		t.Fatal("old secret must be rejected")
	}
	if err := s2.Delete(k.ID); err != nil || s2.Len() != 0 {
		t.Fatal("delete failed")
	}
}

func TestRPM(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "k.json"))
	k, _ := s.Create(Spec{Name: "r", RPM: 2}, "")
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	if s.Allow(k) != nil || s.Allow(k) != nil {
		t.Fatal("first two must pass")
	}
	d := s.Allow(k)
	if d == nil || d.RetryAfter != time.Minute {
		t.Fatalf("third must be limited: %+v", d)
	}
	now = now.Add(61 * time.Second)
	if s.Allow(k) != nil {
		t.Fatal("window must slide")
	}
}
