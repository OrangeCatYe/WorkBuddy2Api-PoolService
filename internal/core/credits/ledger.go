package credits

import (
	"sync"
	"time"
)

// Ledger 按账号、按自然日累计积分消耗（内存；启动时由请求归档重建）。
type Ledger struct {
	mu    sync.RWMutex
	days  map[string]map[string]float64 // provider/account → 2006-01-02 → credit
	reqs  map[string]map[string]int64
	since time.Time // 历史数据起点（最早一条记录或服务启动时间）
}

// NewLedger since 为历史数据起点。
func NewLedger(since time.Time) *Ledger {
	return &Ledger{days: map[string]map[string]float64{}, reqs: map[string]map[string]int64{}, since: since}
}

// Since 历史数据起点。
func (l *Ledger) Since() time.Time {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.since
}

// Add 记一笔消耗。
func (l *Ledger) Add(key string, t time.Time, credit float64) {
	if key == "" {
		return
	}
	d := t.Format("2006-01-02")
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.Before(l.since) {
		l.since = t
	}
	m := l.days[key]
	if m == nil {
		m = map[string]float64{}
		l.days[key] = m
		l.reqs[key] = map[string]int64{}
	}
	m[d] += credit
	l.reqs[key][d]++
}

// Prune 删除 keepDays 天以前的数据。
func (l *Ledger) Prune(now time.Time, keepDays int) {
	cut := now.AddDate(0, 0, -keepDays).Format("2006-01-02")
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range []map[string]map[string]float64{l.days} {
		for _, dm := range m {
			for d := range dm {
				if d < cut {
					delete(dm, d)
				}
			}
		}
	}
	for _, dm := range l.reqs {
		for d := range dm {
			if d < cut {
				delete(dm, d)
			}
		}
	}
}

// Burn 最近 windowDays 天的日均消耗。observed 为实际可用的观测天数
// （服务运行不足一个窗口时按实际时长折算，至少 6 小时，避免短时数据放大过度）。
func (l *Ledger) Burn(key string, now time.Time, windowDays int) (perDay, observed float64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	start := now.AddDate(0, 0, -windowDays)
	if l.since.After(start) {
		start = l.since
	}
	observed = now.Sub(start).Hours() / 24
	if observed < 0.25 {
		observed = 0.25
	}
	// 窗口起始日按整天计入（按日分桶的近似）
	first := start.Format("2006-01-02")
	var sum float64
	for d, v := range l.days[key] {
		if d >= first {
			sum += v
		}
	}
	return sum / observed, observed
}

// Today 今日消耗与请求数。
func (l *Ledger) Today(key string, now time.Time) (float64, int64) {
	d := now.Format("2006-01-02")
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.days[key][d], l.reqs[key][d]
}

// Series 最近 n 天每日消耗（旧 → 新，最后一项为今天）。
func (l *Ledger) Series(key string, now time.Time, n int) []float64 {
	out := make([]float64, n)
	l.mu.RLock()
	defer l.mu.RUnlock()
	m := l.days[key]
	for i := 0; i < n; i++ {
		out[i] = m[now.AddDate(0, 0, i-n+1).Format("2006-01-02")]
	}
	return out
}
