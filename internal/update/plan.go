package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/orangeruan/poolservice/internal/config"
)

const (
	planFile   = "plan.json"
	aliveFile  = "alive"
	resultFile = "result.json"
	applyLog   = "apply.log"
)

// PlanFile 需要落地的一个文件。
type PlanFile struct {
	Rel  string `json:"rel"`  // 相对包根目录，/ 分隔
	Mode string `json:"mode"` // ModeReplace / ModeIfMissing
}

// Plan 主程序交给更新程序的执行计划。更新程序运行时主程序已经退出，
// 所以计划里必须包含重新拉起所需的一切（路径、参数、工作目录、探活地址）。
type Plan struct {
	Schema int `json:"schema"`
	// PID 要等待退出的旧进程。
	PID int `json:"pid"`
	// ExePath 当前主程序路径（会被替换），重启也用这个路径；用户改过文件名也照原样处理。
	ExePath string `json:"exe_path"`
	// MainRel 安装包里主程序的相对路径（poolsvc / poolsvc.exe），它映射到 ExePath。
	MainRel string   `json:"main_rel"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
	// ProbeAddr 探活地址 host:port。
	ProbeAddr   string `json:"probe_addr"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	StageDir    string `json:"stage_dir"`
	UpdateDir   string `json:"update_dir"`
	// ExtraBackups 额外备份的用户文件（config.json、各平台 state.json），仅备份，更新不会改动它们。
	ExtraBackups []string `json:"extra_backups,omitempty"`
	// Visible 新进程是否开新的可见控制台（用户原本是在控制台里运行的）。
	Visible bool `json:"visible"`
	// GraceSeconds 等旧进程自行退出的时间，超时才强制结束。
	GraceSeconds int        `json:"grace_seconds"`
	Files        []PlanFile `json:"files"`
}

// Result 一次更新的结果，写在 result.json，新进程读到后在面板上提示。
type Result struct {
	OK         bool      `json:"ok"`
	RolledBack bool      `json:"rolled_back,omitempty"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Message    string    `json:"message"`
	At         time.Time `json:"at"`
}

// target 包内相对路径对应的安装位置。
func (p *Plan) target(rel string) string {
	if rel == p.MainRel {
		return p.ExePath
	}
	return filepath.Join(filepath.Dir(p.ExePath), filepath.FromSlash(rel))
}

// WritePlan 原子写计划文件。
func WritePlan(path string, p *Plan) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(b, '\n'), 0o600)
}

// LoadPlan 读取并校验计划。
func LoadPlan(path string) (*Plan, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("更新计划解析失败: %w", err)
	}
	switch {
	case p.Schema != 1:
		return nil, fmt.Errorf("不支持的更新计划版本 %d", p.Schema)
	case p.PID <= 0, p.ExePath == "", p.StageDir == "", p.UpdateDir == "", p.MainRel == "", len(p.Files) == 0:
		return nil, fmt.Errorf("更新计划不完整")
	}
	return &p, nil
}

// WriteResult 写结果文件。
func WriteResult(dir string, r Result) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(filepath.Join(dir, resultFile), append(b, '\n'), 0o600)
}

func readResult(dir string) *Result {
	b, err := os.ReadFile(filepath.Join(dir, resultFile))
	if err != nil {
		return nil
	}
	var r Result
	if json.Unmarshal(b, &r) != nil || r.To == "" {
		return nil
	}
	return &r
}
