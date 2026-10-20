// Package agentcfg 把本网关的地址、密钥和模型写进其他 Agent（Claude Code、Codex、OpenCode 等）的配置文件。
//
// 设计要点：
//   - 只认「配置目录」，不找安装位置：软件装在哪里不影响写入，配置目录由环境变量 / 手动指定 / 默认位置依次决定。
//   - 只改自己的：写入的条目带 poolservice 标记，移除时只删带标记的；用户原有内容原样保留（含注释与格式）。
//   - 先算后写：适配器只产出「文件变更」，由 Manager 统一做复读校验、备份、原子写入与失败回滚。
package agentcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	// ProviderID 写进各 Agent 的供应商标识，也是「受管」标记。
	ProviderID = "poolservice"
	// ProviderName 展示名。
	ProviderName = "PoolService"
)

// Model 写入 Agent 的模型描述，口径与网关 /v1/models 一致。
type Model struct {
	ID        string
	Name      string
	Context   int
	MaxOutput int
	Vision    bool
	Tools     bool
	Reasoning bool
	Efforts   []string
}

// Input 一次导入的参数。
type Input struct {
	BaseURL string // 网关根地址，不带 /v1、不带结尾斜杠
	APIKey  string
	Models  []Model
	Default string // 默认模型 ID，空 = 不改 Agent 当前默认
	Fast    string // 快速模型 ID（仅 Claude Code 使用）
	Profile string // DSH / OMP 的档案名
	Dir     string // 用户手动指定的配置目录
}

// OpenAIBase 返回 OpenAI 兼容地址（带 /v1）。
func (in Input) OpenAIBase() string { return strings.TrimRight(in.BaseURL, "/") + "/v1" }

// AnthropicBase 返回 Anthropic 地址（Messages 接口挂在 <base>/v1/messages）。
func (in Input) AnthropicBase() string { return strings.TrimRight(in.BaseURL, "/") }

// Options 检测 / 移除时的可选参数。
type Options struct {
	Profile string
	Dir     string
}

// Choice 一个可选项（档案等）。
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// FileInfo 将被写入的文件。
type FileInfo struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// Info 单个 Agent 的检测结果。
type Info struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Protocol        string     `json:"protocol"` // chat / anthropic / responses
	Installed       bool       `json:"installed"`
	Evidence        []string   `json:"evidence,omitempty"`
	Files           []FileInfo `json:"files"`
	Profiles        []Choice   `json:"profiles,omitempty"`
	Profile         string     `json:"profile,omitempty"`
	Imported        bool       `json:"imported"`
	ImportedModels  []string   `json:"imported_models,omitempty"`
	SupportsDefault bool       `json:"supports_default"`
	NeedsDefault    bool       `json:"needs_default"`
	SupportsFast    bool       `json:"supports_fast,omitempty"`
	Takeover        bool       `json:"takeover"`
	Notes           []string   `json:"notes,omitempty"`
	Restart         string     `json:"restart,omitempty"`
	Blocked         string     `json:"blocked,omitempty"`
}

// Skip 被跳过的模型及原因。
type Skip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Change 一个文件的变更。
type Change struct {
	Path    string
	Before  []byte
	Existed bool
	After   []byte
	Delete  bool
	Snippet string   // 受管片段，用于预览与复制
	Lang    string   // 片段语言：json / toml / yaml
	Secrets []string // Snippet 中需要在预览里遮掩的字符串
}

// PrevVal 被覆盖前的值，移除时用来还原。
type PrevVal struct {
	Present bool            `json:"present"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// Prev 键 → 原值。
type Prev map[string]PrevVal

// Result 适配器计算出的变更。
type Result struct {
	Changes   []Change
	Imported  []string
	Skipped   []Skip
	Warnings  []string
	Prev      Prev // 非空时写入后保存；Remove 时返回空并设置 ClearPrev
	ClearPrev bool
}

// Adapter 一个目标 Agent。
type Adapter interface {
	ID() string
	Name() string
	Detect(env Env, opt Options, prev Prev) Info
	Build(env Env, in Input, prev Prev) (*Result, error)
	Remove(env Env, opt Options, prev Prev) (*Result, error)
}

// All 返回全部适配器，顺序即界面展示顺序。
func All() []Adapter {
	return []Adapter{
		modelsJSON{id: "workbuddy", name: "WorkBuddy", dirName: ".workbuddy", cmds: []string{"workbuddy"}, appDirs: []string{"WorkBuddy", "@genieworkbuddy-desktop-updater"}},
		modelsJSON{id: "codebuddy", name: "CodeBuddy", dirName: ".codebuddy", cmds: []string{"codebuddy"}, appDirs: []string{"CodeBuddy CN", "CodeBuddyExtension", "CodeBuddy"}},
		claudeCode{},
		claudeDesktop{},
		codex{},
		openCode{},
		piAgent{},
		ompAgent{},
		dshAgent{},
	}
}

// Find 按 ID 取适配器。
func Find(id string) Adapter {
	for _, a := range All() {
		if a.ID() == id {
			return a
		}
	}
	return nil
}

// Env 运行环境，可注入以便测试。
type Env struct {
	GOOS         string
	Home         string
	AppData      string
	LocalAppData string
	Getenv       func(string) string
	LookPath     func(string) (string, error)
}

// DefaultEnv 取当前进程环境。
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS:         runtime.GOOS,
		Home:         home,
		AppData:      os.Getenv("APPDATA"),
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		Getenv:       os.Getenv,
		LookPath:     exec.LookPath,
	}
}

func (e Env) env(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return strings.TrimSpace(e.Getenv(k))
}

// path 解析用户给出或环境变量里的路径：支持 ~ 前缀，相对路径按主目录解释。
func (e Env) path(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	switch {
	case raw == "~":
		return e.Home
	case strings.HasPrefix(raw, "~/"), strings.HasPrefix(raw, `~\`):
		return filepath.Join(e.Home, raw[2:])
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Join(e.Home, raw)
}

func (e Env) lookPath(cmds ...string) []string {
	if e.LookPath == nil {
		return nil
	}
	var out []string
	for _, c := range cmds {
		if p, err := e.LookPath(c); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func (e Env) isWindows() bool { return e.GOOS == "windows" }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// readOpt 读文件；不存在返回 (nil, false, nil)。符号链接一律拒绝。
func readOpt(p string) ([]byte, bool, error) {
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("%s 是符号链接，为避免写到意料之外的位置，已拒绝", p)
	}
	if st.IsDir() {
		return nil, false, fmt.Errorf("%s 是目录而不是文件", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func fileInfos(paths ...string) []FileInfo {
	out := make([]FileInfo, 0, len(paths))
	for _, p := range paths {
		out = append(out, FileInfo{Path: p, Exists: exists(p)})
	}
	return out
}

// MaskSecret 遮掩密钥，只留头尾，用于预览。
func MaskSecret(s string) string {
	if len(s) <= 12 {
		return strings.Repeat("•", len(s))
	}
	return s[:8] + "••••" + s[len(s)-4:]
}

func sortedIDs(models []Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out
}

func findModel(models []Model, id string) *Model {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

func displayName(m Model) string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return m.ID
}

func inputModalities(m Model) []string {
	if m.Vision {
		return []string{"text", "image"}
	}
	return []string{"text"}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func removeStr(list []string, s string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// effortSet 返回模型支持的思考档位集合（小写）。
func effortSet(m Model) map[string]bool {
	set := map[string]bool{}
	for _, e := range m.Efforts {
		set[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return set
}

// 去重保序。
func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
