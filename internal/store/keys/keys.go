// Package keys 下游 API Key 管理：模型白名单、积分预算、RPM 限速与用量累计。
//
// 存储为 <data_dir>/keys.json（0600）。密钥以明文保存，便于在面板中再次复制；
// 本服务定位为个人 / 小团队本机网关，文件权限即安全边界。
package keys

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/orangeruan/poolservice/internal/httpx"
)

// Key 一个下游 API Key。
type Key struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Secret    string     `json:"secret"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Note      string     `json:"note,omitempty"`

	// Models 模型白名单，支持 * 通配（匹配短名或 平台/模型）；空 = 全部。
	Models []string `json:"models,omitempty"`
	// RPM 每分钟请求上限；0 = 不限。
	RPM int `json:"rpm,omitempty"`
	// CreditBudget 累计积分预算；0 = 不限。
	CreditBudget float64 `json:"credit_budget,omitempty"`
	// DailyCreditBudget 每日积分预算（本地时区自然日）；0 = 不限。
	DailyCreditBudget float64 `json:"daily_credit_budget,omitempty"`

	Usage Usage `json:"usage"`
}

// Usage 累计用量。
type Usage struct {
	Requests         int64      `json:"requests"`
	Failures         int64      `json:"failures"`
	PromptTokens     int64      `json:"prompt_tokens"`
	CompletionTokens int64      `json:"completion_tokens"`
	CachedTokens     int64      `json:"cached_tokens"`
	Credit           float64    `json:"credit"`
	Day              string     `json:"day,omitempty"` // DayCredit 所属日期
	DayCredit        float64    `json:"day_credit"`
	DayRequests      int64      `json:"day_requests"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
}

// Spec 创建 / 更新参数。
type Spec struct {
	Name              string     `json:"name"`
	Enabled           *bool      `json:"enabled,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	Note              string     `json:"note"`
	Models            []string   `json:"models"`
	RPM               int        `json:"rpm"`
	CreditBudget      float64    `json:"credit_budget"`
	DailyCreditBudget float64    `json:"daily_credit_budget"`
}

// Denied 鉴权 / 限额拒绝原因（网关据此返回不同状态码）。
type Denied struct {
	Status     int
	Code, Msg  string
	RetryAfter time.Duration
}

func (d *Denied) Error() string { return d.Msg }

// Store Key 存储。
type Store struct {
	mu    sync.Mutex
	path  string
	keys  []*Key
	byKey map[string]*Key
	rate  map[string][]time.Time
	dirty bool
	now   func() time.Time
}

// Open 加载（不存在视为空）。
func Open(file string) (*Store, error) {
	s := &Store{path: file, byKey: map[string]*Key{}, rate: map[string][]time.Time{}, now: time.Now}
	raw, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var doc struct {
			Keys []*Key `json:"keys"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("keys: parse %s: %w", file, err)
		}
		s.keys = doc.Keys
	}
	s.reindex()
	return s, nil
}

func (s *Store) reindex() {
	s.byKey = make(map[string]*Key, len(s.keys))
	for _, k := range s.keys {
		s.byKey[k.Secret] = k
	}
}

func (s *Store) saveLocked() error {
	doc := struct {
		Keys []*Key `json:"keys"`
	}{s.keys}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Flush 落盘累计用量（周期调用 / 退出时调用）。
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewSecret 生成 sk-pool- 前缀密钥。
func NewSecret() string { return "sk-pool-" + randHex(20) }

func (sp Spec) normalize() (Spec, error) {
	sp.Name = strings.TrimSpace(sp.Name)
	if sp.Name == "" {
		return sp, errors.New("名称不能为空")
	}
	if sp.RPM < 0 || sp.CreditBudget < 0 || sp.DailyCreditBudget < 0 {
		return sp, errors.New("限额不能为负数")
	}
	var models []string
	for _, m := range sp.Models {
		if m = strings.TrimSpace(m); m != "" {
			if _, err := path.Match(strings.ToLower(m), ""); err != nil {
				return sp, fmt.Errorf("模型规则 %q 无效", m)
			}
			models = append(models, m)
		}
	}
	sp.Models = models
	return sp, nil
}

func apply(k *Key, sp Spec) {
	k.Name, k.Note, k.Models = sp.Name, sp.Note, sp.Models
	k.RPM, k.CreditBudget, k.DailyCreditBudget = sp.RPM, sp.CreditBudget, sp.DailyCreditBudget
	k.ExpiresAt = sp.ExpiresAt
	if sp.Enabled != nil {
		k.Enabled = *sp.Enabled
	}
}

// Create 新建；secret 为空时自动生成。
func (s *Store) Create(sp Spec, secret string) (Key, error) {
	sp, err := sp.normalize()
	if err != nil {
		return Key{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if secret == "" {
		secret = NewSecret()
	}
	if _, dup := s.byKey[secret]; dup {
		return Key{}, errors.New("密钥已存在")
	}
	k := &Key{ID: "key_" + randHex(6), Secret: secret, Enabled: true, CreatedAt: s.now()}
	apply(k, sp)
	s.keys = append(s.keys, k)
	s.reindex()
	if err := s.saveLocked(); err != nil {
		return Key{}, err
	}
	return *k, nil
}

// Update 修改设置（不改密钥与用量）。
func (s *Store) Update(id string, sp Spec) (Key, error) {
	sp, err := sp.normalize()
	if err != nil {
		return Key{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.findLocked(id)
	if k == nil {
		return Key{}, errors.New("密钥不存在")
	}
	apply(k, sp)
	return *k, s.saveLocked()
}

// Rotate 重新生成密钥。
func (s *Store) Rotate(id string) (Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.findLocked(id)
	if k == nil {
		return Key{}, errors.New("密钥不存在")
	}
	k.Secret = NewSecret()
	s.reindex()
	return *k, s.saveLocked()
}

// ResetUsage 清零用量。
func (s *Store) ResetUsage(id string) (Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.findLocked(id)
	if k == nil {
		return Key{}, errors.New("密钥不存在")
	}
	k.Usage = Usage{}
	return *k, s.saveLocked()
}

// Delete 删除。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range s.keys {
		if k.ID == id {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			s.reindex()
			delete(s.rate, id)
			return s.saveLocked()
		}
	}
	return errors.New("密钥不存在")
}

func (s *Store) findLocked(id string) *Key {
	for _, k := range s.keys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

// Get 按 ID 取快照。
func (s *Store) Get(id string) (Key, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k := s.findLocked(id); k != nil {
		s.rollDayLocked(k)
		return *k, true
	}
	return Key{}, false
}

// List 全部（按创建时间）。
func (s *Store) List() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Key, 0, len(s.keys))
	for _, k := range s.keys {
		s.rollDayLocked(k)
		out = append(out, *k)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Len 数量。
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}

// HasSecret 密钥是否已存在。
func (s *Store) HasSecret(secret string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.byKey[secret]
	return ok
}

func (s *Store) rollDayLocked(k *Key) {
	if day := s.now().Format("2006-01-02"); k.Usage.Day != day {
		k.Usage.Day, k.Usage.DayCredit, k.Usage.DayRequests = day, 0, 0
	}
}

// Authenticate 按密钥查找并检查启用、过期、预算（不计 RPM）。
func (s *Store) Authenticate(secret string) (Key, *Denied) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var k *Key
	for sec, kk := range s.byKey {
		if httpx.SecretEqual(sec, secret) {
			k = kk
			break
		}
	}
	if k == nil {
		return Key{}, &Denied{Status: 401, Code: "invalid_api_key", Msg: "invalid API key"}
	}
	if !k.Enabled {
		return Key{}, &Denied{Status: 401, Code: "key_disabled", Msg: "API key is disabled"}
	}
	now := s.now()
	if k.ExpiresAt != nil && now.After(*k.ExpiresAt) {
		return Key{}, &Denied{Status: 401, Code: "key_expired", Msg: "API key has expired"}
	}
	s.rollDayLocked(k)
	if k.CreditBudget > 0 && k.Usage.Credit >= k.CreditBudget {
		return Key{}, &Denied{Status: 429, Code: "insufficient_quota",
			Msg: fmt.Sprintf("API key credit budget exhausted (%.2f / %.2f)", k.Usage.Credit, k.CreditBudget)}
	}
	if k.DailyCreditBudget > 0 && k.Usage.DayCredit >= k.DailyCreditBudget {
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		return Key{}, &Denied{Status: 429, Code: "insufficient_quota", RetryAfter: next.Sub(now),
			Msg: fmt.Sprintf("API key daily credit budget exhausted (%.2f / %.2f)", k.Usage.DayCredit, k.DailyCreditBudget)}
	}
	return *k, nil
}

// Allow RPM 滑动窗口限速；通过时记一次。
func (s *Store) Allow(k Key) *Denied {
	if k.RPM <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	win := s.rate[k.ID]
	cut := 0
	for cut < len(win) && now.Sub(win[cut]) >= time.Minute {
		cut++
	}
	win = win[cut:]
	if len(win) >= k.RPM {
		retry := time.Minute - now.Sub(win[0])
		s.rate[k.ID] = win
		return &Denied{Status: 429, Code: "rate_limit_exceeded", RetryAfter: retry,
			Msg: fmt.Sprintf("API key rate limit exceeded (%d requests per minute)", k.RPM)}
	}
	s.rate[k.ID] = append(win, now)
	return nil
}

// AllowsModel 模型白名单匹配（大小写不敏感，支持 *）。
func (k Key) AllowsModel(names ...string) bool {
	if len(k.Models) == 0 {
		return true
	}
	for _, pat := range k.Models {
		pat = strings.ToLower(pat)
		for _, n := range names {
			if ok, _ := path.Match(pat, strings.ToLower(n)); ok {
				return true
			}
		}
	}
	return false
}

// Record 累计一次请求的用量。
func (s *Store) Record(id string, ok bool, prompt, completion, cached int64, credit float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.findLocked(id)
	if k == nil {
		return
	}
	s.rollDayLocked(k)
	now := s.now()
	u := &k.Usage
	u.Requests++
	u.DayRequests++
	if !ok {
		u.Failures++
	}
	u.PromptTokens += prompt
	u.CompletionTokens += completion
	u.CachedTokens += cached
	u.Credit += credit
	u.DayCredit += credit
	u.LastUsedAt = &now
	s.dirty = true
}
