// Command migrate 把旧版 workbuddy2api-panel 的账号与运行态迁入本项目。
//
// 用法：
//
//	go run ./cmd/migrate -from <旧版目录> [-data <本项目 data 目录>] [-dry-run] [-force]
//
// 迁移内容：
//  1. auths/*.json 凭证（嵌套形与扁平形都支持，复用 auth.Parse 的双形态解析）
//  2. data/state.json 的账号运行态（余额 / 禁用 / 冷却 / 统计 / 模型冷却 / 成本账本）
//  3. 迁移前自动备份现有 auths 与 state.json 到 data/backup/
//
// 冲突策略（默认）：
//   - 同 UID 已存在时保留**令牌较新**的一方（expiresAt 更大），避免用过期凭证覆盖有效凭证
//   - -force 改为旧版无条件覆盖
//
// 本命令只读写本地文件，不访问上游、不发请求、不消耗积分。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
)

// legacyState 旧版 data/state.json 的结构（只取需要的字段，未知字段忽略）。
type legacyState struct {
	Accounts map[string]json.RawMessage `json:"accounts"`
}

// legacyAccount 旧版单个账号状态。字段名与旧版一致（下划线），
// 与新版 stateAccount 同构时直接拷贝 JSON，避免逐字段翻译出错。
type legacyAccount struct {
	Credits                  int64  `json:"credits"`
	CreditsTotal             int64  `json:"credits_total"`
	Disabled                 bool   `json:"disabled"`
	Paused                   bool   `json:"paused"`
	Reason                   string `json:"reason"`
	Until                    string `json:"until"`
	CoolKind                 int    `json:"cool_kind"`
	SuccessCount             int64  `json:"success_count"`
	ErrTotal                 int64  `json:"err_total"`
	ErrCount                 int    `json:"err_count"`
	LastSuccess              string `json:"last_success"`
	LastErr                  string `json:"last_err"`
	LastCheckinDay           string `json:"last_checkin_day"`
	SoftStreak               int    `json:"soft_streak"`
	SessionDeadFails         int    `json:"session_dead_fails"`
	ConsecutiveFails         int    `json:"consecutive_fails"`
	CreditsExpiring          int64  `json:"credits_expiring"`
	CreditsEarliestExpiry    string `json:"credits_earliest_expiry"`
	CreditsEarliestRemaining int64  `json:"credits_earliest_remaining"`
}

type options struct {
	from    string
	dataDir string
	dryRun  bool
	force   bool
}

func main() {
	var o options
	flag.StringVar(&o.from, "from", "", "旧版 workbuddy2api-panel 目录（必填）")
	flag.StringVar(&o.dataDir, "data", "data", "本项目 data 目录")
	flag.BoolVar(&o.dryRun, "dry-run", false, "只打印计划，不写任何文件")
	flag.BoolVar(&o.force, "force", false, "同 UID 冲突时旧版无条件覆盖（默认保留令牌较新的一方）")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "migrate: "+err.Error())
		os.Exit(1)
	}
}

func run(o options) error {
	if o.from == "" {
		return fmt.Errorf("缺少 -from 参数：请指向旧版目录")
	}
	srcAuths := filepath.Join(o.from, "auths")
	srcState := filepath.Join(o.from, "data", "state.json")
	if _, err := os.Stat(srcAuths); err != nil {
		return fmt.Errorf("旧版 auths 目录不可读: %w", err)
	}

	dstAuths := filepath.Join(o.dataDir, "workbuddy", "auths")
	dstState := filepath.Join(o.dataDir, "workbuddy", "state.json")

	fmt.Printf("来源: %s\n", o.from)
	fmt.Printf("目标: %s\n", o.dataDir)
	if o.dryRun {
		fmt.Println("模式: dry-run（不写文件）")
	}

	// 1) 读取旧版凭证
	srcFiles, err := filepath.Glob(filepath.Join(srcAuths, "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(srcFiles)
	type srcEntry struct {
		path string
		a    *auth.Auth
	}
	var srcs []srcEntry
	for _, f := range srcFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			fmt.Printf("  跳过 %s: 读取失败 %v\n", filepath.Base(f), err)
			continue
		}
		a, err := auth.Parse(raw)
		if err != nil {
			fmt.Printf("  跳过 %s: 解析失败 %v\n", filepath.Base(f), err)
			continue
		}
		srcs = append(srcs, srcEntry{path: f, a: a})
	}
	fmt.Printf("\n旧版凭证: %d 个\n", len(srcs))

	// 2) 读取目标已有凭证，建立 UID -> Auth 索引
	existing := map[string]*auth.Auth{}
	if dstFiles, err := filepath.Glob(filepath.Join(dstAuths, "workbuddy*.json")); err == nil {
		for _, f := range dstFiles {
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			a, err := auth.Parse(raw)
			if err != nil {
				continue
			}
			a.FilePath = f
			existing[a.UID] = a
		}
	}
	fmt.Printf("目标已有: %d 个\n", len(existing))

	// 3) 决策每个账号的处置
	type plan struct {
		src      srcEntry
		conflict *auth.Auth
		take     bool // true = 采用旧版
		reason   string
	}
	var plans []plan
	for _, s := range srcs {
		p := plan{src: s}
		old := existing[s.a.UID]
		switch {
		case old == nil:
			p.take = true
			p.reason = "新增"
		case o.force:
			p.conflict = old
			p.take = true
			p.reason = "覆盖（-force）"
		case s.a.ExpiresAt > old.ExpiresAt:
			p.conflict = old
			p.take = true
			p.reason = fmt.Sprintf("旧版令牌更新（%s > %s）",
				ts(s.a.ExpiresAt), ts(old.ExpiresAt))
		default:
			p.conflict = old
			p.take = false
			p.reason = fmt.Sprintf("保留目标（其令牌不旧于旧版 %s）", ts(old.ExpiresAt))
		}
		plans = append(plans, p)
	}

	fmt.Println("\n处置计划：")
	for _, p := range plans {
		name := p.src.a.Nickname
		if name == "" {
			name = "(无昵称)"
		}
		fmt.Printf("  %s  uid=%s  %s\n", name, short(p.src.a.UID), p.reason)
	}

	// 4) 读取旧版运行态
	states := map[string]legacyAccount{}
	if raw, err := os.ReadFile(srcState); err == nil {
		var ls legacyState
		if err := json.Unmarshal(raw, &ls); err != nil {
			fmt.Printf("\n警告: 旧版 state.json 解析失败，跳过运行态迁移: %v\n", err)
		} else {
			for uid, rawAcc := range ls.Accounts {
				var la legacyAccount
				if err := json.Unmarshal(rawAcc, &la); err != nil {
					fmt.Printf("  警告: 账号 %s 状态解析失败，跳过: %v\n", short(uid), err)
					continue
				}
				states[uid] = la
			}
			fmt.Printf("\n旧版运行态: %d 个账号\n", len(states))
		}
	} else {
		fmt.Printf("\n旧版 state.json 不存在，跳过运行态迁移\n")
	}

	if o.dryRun {
		fmt.Println("\ndry-run 结束，未写入任何文件")
		return nil
	}

	// 5) 备份
	stamp := time.Now().Format("20060102-150405")
	backupDir := filepath.Join(o.dataDir, "backup", "migrate-"+stamp)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("创建备份目录失败: %w", err)
	}
	if err := copyDir(dstAuths, filepath.Join(backupDir, "auths")); err != nil {
		fmt.Printf("  警告: 备份 auths 失败: %v\n", err)
	}
	if raw, err := os.ReadFile(dstState); err == nil {
		_ = os.WriteFile(filepath.Join(backupDir, "state.json"), raw, 0o600)
	}
	fmt.Printf("\n已备份到 %s\n", backupDir)

	// 6) 写凭证
	if err := os.MkdirAll(dstAuths, 0o755); err != nil {
		return err
	}
	written := 0
	for _, p := range plans {
		if !p.take {
			continue
		}
		a := p.src.a
		a.FilePath = filepath.Join(dstAuths, "workbuddy-"+a.UID+".json")
		// realm 缺省时按 domain 推断补齐（与 LoadDir 的存量迁移一致）。
		if a.RealmStored() == "" {
			_, _ = a.BackfillRealm()
		}
		if err := a.SaveAtomic(); err != nil {
			fmt.Printf("  失败: %s 写入失败 %v\n", short(a.UID), err)
			continue
		}
		written++
		// 新写入的账号成为"已有"，供后续状态合并使用。
		existing[a.UID] = a
	}
	fmt.Printf("写入凭证: %d 个\n", written)

	// 7) 合并运行态
	merged, err := mergeState(dstState, states, existing)
	if err != nil {
		return err
	}
	fmt.Printf("合并运行态: %d 个账号（其中来自旧版 %d 个）\n", merged.total, merged.fromLegacy)

	fmt.Println("\n迁移完成。请重启服务使新账号生效。")
	return nil
}

// mergeState 把旧版账号运行态合并进目标 state.json。
// 只补充目标缺失的 UID；已存在的 UID 保留目标现状（运行态由新版自己维护，更可信）。
func mergeState(dstState string, legacy map[string]legacyAccount, existing map[string]*auth.Auth) (struct {
	total      int
	fromLegacy int
}, error) {
	var out struct {
		total      int
		fromLegacy int
	}

	// 读目标现状
	dst := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(dstState); err == nil {
		var sf struct {
			Accounts map[string]json.RawMessage `json:"accounts"`
		}
		if err := json.Unmarshal(raw, &sf); err == nil && sf.Accounts != nil {
			dst = sf.Accounts
		}
	}

	// 只迁移目标里确实存在的 UID（凭证已就位），避免写入孤儿状态。
	for uid, la := range legacy {
		if _, ok := existing[uid]; !ok {
			continue
		}
		if _, exists := dst[uid]; exists {
			continue // 已有运行态，保留新版现状
		}
		converted, err := convertAccount(la)
		if err != nil {
			fmt.Printf("  警告: 账号 %s 状态转换失败，跳过: %v\n", short(uid), err)
			continue
		}
		dst[uid] = converted
		out.fromLegacy++
	}
	out.total = len(dst)

	if out.fromLegacy == 0 {
		fmt.Println("  运行态无需合并（目标已覆盖全部旧版账号）")
		return out, nil
	}

	doc := map[string]any{"accounts": dst}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return out, err
	}
	if err := os.MkdirAll(filepath.Dir(dstState), 0o755); err != nil {
		return out, err
	}
	tmp := dstState + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return out, err
	}
	return out, os.Rename(tmp, dstState)
}

// convertAccount 把旧版账号状态转成新版 stateAccount 的 JSON。
// 时间字段旧版是字符串，统一解析后按新版格式输出。
func convertAccount(la legacyAccount) (json.RawMessage, error) {
	out := map[string]any{}
	out["credits"] = la.Credits
	if la.CreditsTotal > 0 {
		out["credits_total"] = la.CreditsTotal
	}
	if la.Disabled {
		out["disabled"] = true
		if la.Reason != "" {
			out["reason"] = la.Reason
		}
	}
	if la.Paused {
		out["paused"] = true
	}
	if t, ok := parseTime(la.Until); ok {
		out["until"] = t
		out["cool_kind"] = la.CoolKind
	}
	if la.SuccessCount > 0 {
		out["success_count"] = la.SuccessCount
	}
	errTotal := la.ErrTotal
	if int64(la.ErrCount) > errTotal {
		errTotal = int64(la.ErrCount) // 旧版 err_count 一次性映射（与 pool 加载逻辑一致）
	}
	if errTotal > 0 {
		out["err_total"] = errTotal
	}
	if t, ok := parseTime(la.LastSuccess); ok {
		out["last_success"] = t
	}
	if t, ok := parseTime(la.LastErr); ok {
		out["last_err"] = t
	}
	if la.LastCheckinDay != "" {
		out["last_checkin_day"] = la.LastCheckinDay
	}
	out["soft_streak"] = la.SoftStreak
	out["session_dead_fails"] = la.SessionDeadFails
	out["consecutive_fails"] = la.ConsecutiveFails
	out["credits_expiring"] = la.CreditsExpiring
	if t, ok := parseTime(la.CreditsEarliestExpiry); ok {
		out["credits_earliest_expiry"] = t
		out["credits_earliest_remaining"] = la.CreditsEarliestRemaining
	}
	return json.Marshal(out)
}

// parseTime 解析旧版时间字符串；空串或解析失败返回 false。
func parseTime(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil && !t.IsZero() {
			return t.Format(time.RFC3339Nano), true
		}
	}
	return "", false
}

// ts 把 Unix 秒格式化为可读时间；0 或非法显示为 unknown。
func ts(unix int64) string {
	if unix <= 0 {
		return "unknown"
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04")
}

func short(uid string) string {
	if len(uid) > 8 {
		return uid[:8]
	}
	return uid
}

// copyDir 递归复制目录（备份用）。
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 源不存在（首次迁移）不算错误
		}
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}
