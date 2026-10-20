package logfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateKeepsBoundedHistory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "logs", "a.log")
	w, err := Open(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	line := strings.Repeat("x", 40) + "\n" // 41 字节：每 2 行触发一次滚动
	for i := 0; i < 12; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a.log", "a.log.1", "a.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, "logs", name)); err != nil {
			t.Errorf("应存在 %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "logs", "a.log.3")); err == nil {
		t.Error("只应保留 2 份历史，不应出现 a.log.3")
	}
	fi, _ := os.Stat(p)
	if fi.Size() > 100 {
		t.Errorf("当前日志不应超过上限: %d", fi.Size())
	}
}

func TestReopenAppendsAndCountsExistingSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	w, _ := Open(p, 1000, 1)
	w.Write([]byte("first\n"))
	w.Close()
	w, _ = Open(p, 1000, 1)
	defer w.Close()
	w.Write([]byte("second\n"))
	b, _ := os.ReadFile(p)
	if string(b) != "first\nsecond\n" {
		t.Fatalf("重新打开应追加: %q", b)
	}
}

type badWriter struct{}

func (badWriter) Write([]byte) (int, error) { return 0, errors.New("invalid handle") }

func TestTeeIgnoresBrokenTarget(t *testing.T) {
	var buf bytes.Buffer
	n, err := Tee(badWriter{}, &buf).Write([]byte("hello"))
	if err != nil || n != 5 || buf.String() != "hello" {
		t.Fatalf("坏的写入目标不得影响其他目标: n=%d err=%v buf=%q", n, err, buf.String())
	}
}
