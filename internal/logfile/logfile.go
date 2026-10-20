// Package logfile 带大小滚动的日志文件。主程序在没有控制台时（开机自启、后台运行）
// 把日志写到这里，否则无窗口运行时日志会直接丢失。
package logfile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Writer 追加写文件，超过 max 字节时滚动：poolsvc.log → poolsvc.log.1 → .2 …，最多保留 keep 份历史。
type Writer struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

// Open 打开（必要时创建）日志文件。
func Open(path string, max int64, keep int) (*Writer, error) {
	if max <= 0 {
		max = 5 << 20
	}
	if keep < 0 {
		keep = 0
	}
	w := &Writer{path: path, max: max, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, fi.Size()
	return nil
}

func (w *Writer) rotate() error {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	if w.keep == 0 {
		_ = os.Remove(w.path)
	} else {
		_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.keep))
		for i := w.keep - 1; i >= 1; i-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
		}
		_ = os.Rename(w.path, w.path+".1")
	}
	return w.open()
}

// Write 实现 io.Writer。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close 关闭文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Tee 同时写多个目标，任何一个目标出错都不影响其他目标，也不向调用方报错。
// （标准库 io.MultiWriter 遇到第一个错误就停，无控制台时 stderr 写失败会连带吞掉文件日志。）
func Tee(ws ...io.Writer) io.Writer { return tee(ws) }

type tee []io.Writer

func (t tee) Write(p []byte) (int, error) {
	for _, w := range t {
		_, _ = w.Write(p)
	}
	return len(p), nil
}
