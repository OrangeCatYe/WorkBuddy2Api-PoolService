// Package requests 网关请求日志：内存环形缓冲供面板实时查询，按日 JSONL 归档供长期统计。
//
// 只记录元数据（模型、账号、token、积分、耗时、状态），不记录提示词与回答正文。
package requests

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry 一条请求记录。
type Entry struct {
	ID             string    `json:"id"`
	Time           time.Time `json:"time"`
	Protocol       string    `json:"protocol"` // chat / messages / responses
	KeyID          string    `json:"key_id,omitempty"`
	KeyName        string    `json:"key_name,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	AccountID      string    `json:"account_id,omitempty"`
	Model          string    `json:"model,omitempty"`
	RequestedModel string    `json:"requested_model,omitempty"`
	Stream         bool      `json:"stream"`
	Effort         string    `json:"effort,omitempty"`
	EffortAsked    string    `json:"effort_requested,omitempty"`
	Status         int       `json:"status"`
	OK             bool      `json:"ok"`
	ErrorKind      string    `json:"error_kind,omitempty"`
	Error          string    `json:"error,omitempty"`
	Attempts       int       `json:"attempts,omitempty"`

	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens,omitempty"`
	Credit           float64 `json:"credit"`
	HasCredit        bool    `json:"has_credit,omitempty"`

	TTFBMs     int64  `json:"ttfb_ms"`
	DurationMs int64  `json:"duration_ms"`
	ClientIP   string `json:"client_ip,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
}

// Filter 查询条件。
type Filter struct {
	Provider, Model, KeyID, AccountID, Protocol string
	Status                                      string // ok / error / ""
	Q                                           string
	Limit                                       int
	Before                                      time.Time
}

// Config 配置。
type Config struct {
	Dir           string // 空 = 不归档
	Capacity      int    // 内存条数，缺省 5000
	RetentionDays int    // 缺省 30
}

// Recorder 记录器。
type Recorder struct {
	cfg  Config
	mu   sync.RWMutex
	ring []Entry
	next int
	full bool

	fileMu  sync.Mutex
	day     string
	file    *os.File
	writer  *bufio.Writer
	lastGC  time.Time
	closing bool
}

// New 创建并加载当天归档到内存（重启后面板仍能看到近期记录）。
func New(cfg Config) *Recorder {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 5000
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 30
	}
	r := &Recorder{cfg: cfg, ring: make([]Entry, cfg.Capacity)}
	r.preload()
	return r
}

func (r *Recorder) dayFile(day string) string {
	return filepath.Join(r.cfg.Dir, "requests-"+day+".jsonl")
}

func (r *Recorder) preload() {
	if r.cfg.Dir == "" {
		return
	}
	now := time.Now()
	var all []Entry
	for _, d := range []time.Time{now.AddDate(0, 0, -1), now} {
		f, err := os.Open(r.dayFile(d.Format("2006-01-02")))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				all = append(all, e)
			}
		}
		f.Close()
	}
	if len(all) > r.cfg.Capacity {
		all = all[len(all)-r.cfg.Capacity:]
	}
	for _, e := range all {
		r.push(e)
	}
}

func (r *Recorder) push(e Entry) {
	r.ring[r.next] = e
	r.next = (r.next + 1) % len(r.ring)
	if r.next == 0 {
		r.full = true
	}
}

// Add 记录一条。
func (r *Recorder) Add(e Entry) {
	r.mu.Lock()
	r.push(e)
	r.mu.Unlock()
	r.archive(e)
}

func (r *Recorder) archive(e Entry) {
	if r.cfg.Dir == "" {
		return
	}
	r.fileMu.Lock()
	defer r.fileMu.Unlock()
	if r.closing {
		return
	}
	day := e.Time.Format("2006-01-02")
	if day != r.day || r.file == nil {
		r.closeFileLocked()
		if err := os.MkdirAll(r.cfg.Dir, 0o700); err != nil {
			return
		}
		f, err := os.OpenFile(r.dayFile(day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		r.day, r.file, r.writer = day, f, bufio.NewWriterSize(f, 32<<10)
		r.gcLocked()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = r.writer.Write(append(b, '\n'))
	_ = r.writer.Flush()
}

func (r *Recorder) closeFileLocked() {
	if r.writer != nil {
		_ = r.writer.Flush()
	}
	if r.file != nil {
		_ = r.file.Close()
	}
	r.file, r.writer = nil, nil
}

func (r *Recorder) gcLocked() {
	if time.Since(r.lastGC) < time.Hour {
		return
	}
	r.lastGC = time.Now()
	cut := time.Now().AddDate(0, 0, -r.cfg.RetentionDays).Format("2006-01-02")
	files, _ := filepath.Glob(filepath.Join(r.cfg.Dir, "requests-*.jsonl"))
	for _, f := range files {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "requests-"), ".jsonl")
		if day < cut {
			_ = os.Remove(f)
		}
	}
}

// ScanArchive 依时间顺序遍历 since 以来的归档记录（启动时重建统计用）。
func (r *Recorder) ScanArchive(since time.Time, fn func(Entry)) {
	if r.cfg.Dir == "" {
		return
	}
	r.fileMu.Lock()
	if r.writer != nil {
		_ = r.writer.Flush()
	}
	r.fileMu.Unlock()
	files, _ := filepath.Glob(filepath.Join(r.cfg.Dir, "requests-*.jsonl"))
	sort.Strings(files)
	first := since.Format("2006-01-02")
	for _, file := range files {
		d := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), "requests-"), ".jsonl")
		if d < first {
			continue
		}
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil && !e.Time.Before(since) {
				fn(e)
			}
		}
		f.Close()
	}
}

// Close 刷盘关闭。
func (r *Recorder) Close() {
	r.fileMu.Lock()
	defer r.fileMu.Unlock()
	r.closing = true
	r.closeFileLocked()
}

// snapshot 由新到旧。
func (r *Recorder) snapshot() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := r.next
	if r.full {
		n = len(r.ring)
	}
	out := make([]Entry, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, r.ring[(r.next-i+len(r.ring))%len(r.ring)])
	}
	return out
}

func (f Filter) match(e Entry) bool {
	if f.Provider != "" && e.Provider != f.Provider ||
		f.Model != "" && e.Model != f.Model ||
		f.KeyID != "" && e.KeyID != f.KeyID ||
		f.AccountID != "" && e.AccountID != f.AccountID ||
		f.Protocol != "" && e.Protocol != f.Protocol {
		return false
	}
	switch f.Status {
	case "ok":
		if !e.OK {
			return false
		}
	case "error":
		if e.OK {
			return false
		}
	}
	if !f.Before.IsZero() && !e.Time.Before(f.Before) {
		return false
	}
	if q := strings.ToLower(f.Q); q != "" {
		hay := strings.ToLower(e.ID + " " + e.Model + " " + e.RequestedModel + " " + e.AccountID + " " + e.KeyName + " " + e.Error)
		if !strings.Contains(hay, q) {
			return false
		}
	}
	return true
}

// Query 由新到旧查询内存记录。
func (r *Recorder) Query(f Filter) []Entry {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	out := make([]Entry, 0, f.Limit)
	for _, e := range r.snapshot() {
		if f.match(e) {
			out = append(out, e)
			if len(out) >= f.Limit {
				break
			}
		}
	}
	return out
}

// SumKey 汇总内存中某个 Key 的全部记录（内置密钥不进 Key 存储，用它展示用量）。
func (r *Recorder) SumKey(id string) (b Bucket, last time.Time) {
	for _, e := range r.snapshot() {
		if e.KeyID != id {
			continue
		}
		if last.IsZero() {
			last = e.Time
		}
		b.add(e)
	}
	return b, last
}

// Bucket 统计桶。
type Bucket struct {
	Key              string  `json:"key"`
	Requests         int64   `json:"requests"`
	Failures         int64   `json:"failures"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	Credit           float64 `json:"credit"`
	AvgTTFBMs        int64   `json:"avg_ttfb_ms"`
	ttfbSum, ttfbN   int64
}

func (b *Bucket) add(e Entry) {
	b.Requests++
	if !e.OK {
		b.Failures++
	}
	b.PromptTokens += e.PromptTokens
	b.CompletionTokens += e.CompletionTokens
	b.CachedTokens += e.CachedTokens
	b.Credit += e.Credit
	if e.OK && e.TTFBMs > 0 {
		b.ttfbSum += e.TTFBMs
		b.ttfbN++
		b.AvgTTFBMs = b.ttfbSum / b.ttfbN
	}
}

// Stats 统计结果。
type Stats struct {
	Since   time.Time `json:"since"`
	Total   Bucket    `json:"total"`
	Series  []Bucket  `json:"series"` // 按小时
	ByModel []Bucket  `json:"by_model"`
	ByKey   []Bucket  `json:"by_key"`
	ByAcct  []Bucket  `json:"by_account"`
	ByProto []Bucket  `json:"by_protocol"`
}

// Stats 统计最近 hours 小时（基于内存记录）。
func (r *Recorder) Stats(hours int) Stats {
	if hours <= 0 || hours > 24*7 {
		hours = 24
	}
	now := time.Now()
	since := now.Truncate(time.Hour).Add(-time.Duration(hours-1) * time.Hour)
	st := Stats{Since: since, Total: Bucket{Key: "total"}}
	series := make([]Bucket, hours)
	for i := range series {
		series[i].Key = since.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
	}
	group := func(m map[string]*Bucket, k string, e Entry) {
		if k == "" {
			k = "-"
		}
		b := m[k]
		if b == nil {
			b = &Bucket{Key: k}
			m[k] = b
		}
		b.add(e)
	}
	byModel, byKey, byAcct, byProto := map[string]*Bucket{}, map[string]*Bucket{}, map[string]*Bucket{}, map[string]*Bucket{}
	for _, e := range r.snapshot() {
		if e.Time.Before(since) {
			continue
		}
		st.Total.add(e)
		if i := int(e.Time.Sub(since) / time.Hour); i >= 0 && i < hours {
			series[i].add(e)
		}
		group(byModel, e.Model, e)
		name := e.KeyName
		if name == "" {
			name = e.KeyID
		}
		group(byKey, name, e)
		acct := e.AccountID
		if acct != "" {
			acct = e.Provider + "/" + acct
		}
		group(byAcct, acct, e)
		group(byProto, e.Protocol, e)
	}
	st.Series = series
	flat := func(m map[string]*Bucket) []Bucket {
		out := make([]Bucket, 0, len(m))
		for _, b := range m {
			out = append(out, *b)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Credit != out[j].Credit {
				return out[i].Credit > out[j].Credit
			}
			return out[i].Requests > out[j].Requests
		})
		return out
	}
	st.ByModel, st.ByKey, st.ByAcct, st.ByProto = flat(byModel), flat(byKey), flat(byAcct), flat(byProto)
	return st
}
