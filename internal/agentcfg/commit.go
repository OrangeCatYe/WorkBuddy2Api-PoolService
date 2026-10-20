package agentcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// keepBackups 每个 Agent 保留的备份份数。
const keepBackups = 10

// Manager 负责把适配器算出的变更安全地落盘。
//
// 保护措施（按顺序）：
//  1. 复读校验：文件内容必须仍与预览时一致，被别的程序改过就拒绝；
//  2. 拒绝符号链接；
//  3. 先把原文件备份到数据目录，再写入；
//  4. 同目录临时文件 + 原子重命名；
//  5. 多文件中途失败时，把已写的文件还原。
type Manager struct {
	// Dir 数据目录，备份与还原记录放在这里。
	Dir string

	mu  sync.Mutex
	now func() time.Time
}

// NewManager 创建。
func NewManager(dataDir string) *Manager {
	return &Manager{Dir: dataDir, now: time.Now}
}

// Applied 一次写入的结果。
type Applied struct {
	BackupDir string   `json:"backup_dir,omitempty"`
	Written   []string `json:"written"`
}

// ErrChanged 文件在预览之后被别的程序修改。
var ErrChanged = errors.New("配置文件在预览之后被其他程序修改，请重新预览后再写入")

func (m *Manager) prevPath(agent string) string {
	return filepath.Join(m.Dir, "agent-import", agent+".json")
}

// LoadPrev 读取还原记录；没有记录返回空。
func (m *Manager) LoadPrev(agent string) Prev {
	raw, err := os.ReadFile(m.prevPath(agent))
	if err != nil {
		return nil
	}
	var doc struct {
		Prev Prev `json:"prev"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	return doc.Prev
}

func (m *Manager) savePrev(agent string, prev Prev) error {
	if err := os.MkdirAll(filepath.Dir(m.prevPath(agent)), 0o700); err != nil {
		return err
	}
	doc := struct {
		Agent   string    `json:"agent"`
		SavedAt time.Time `json:"saved_at"`
		Prev    Prev      `json:"prev"`
	}{agent, m.now(), prev}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(m.prevPath(agent), raw, 0o600)
}

func (m *Manager) clearPrev(agent string) { _ = os.Remove(m.prevPath(agent)) }

// Apply 落盘。res 中没有实际变化的文件会被跳过。
func (m *Manager) Apply(agent string, res *Result) (*Applied, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var todo []Change
	for _, c := range res.Changes {
		if !filepath.IsAbs(c.Path) {
			return nil, fmt.Errorf("内部错误：变更路径不是绝对路径：%s", c.Path)
		}
		cur, existed, err := readOpt(c.Path)
		if err != nil {
			return nil, err
		}
		if existed != c.Existed || !bytes.Equal(cur, c.Before) {
			return nil, ErrChanged
		}
		if c.Delete {
			if !existed {
				continue
			}
		} else if existed && bytes.Equal(cur, c.After) {
			continue
		}
		todo = append(todo, c)
	}
	out := &Applied{Written: []string{}}
	if len(todo) == 0 {
		return out, m.finish(agent, res)
	}

	backupDir, err := m.backup(agent, todo)
	if err != nil {
		return nil, fmt.Errorf("备份原配置失败，未写入任何文件：%w", err)
	}
	out.BackupDir = backupDir

	var done []Change
	for _, c := range todo {
		if err := applyOne(c); err != nil {
			for i := len(done) - 1; i >= 0; i-- {
				_ = revertOne(done[i])
			}
			return nil, fmt.Errorf("写入 %s 失败，已还原本次已写的文件：%w", c.Path, err)
		}
		done = append(done, c)
		out.Written = append(out.Written, c.Path)
	}
	m.prune(agent)
	return out, m.finish(agent, res)
}

func (m *Manager) finish(agent string, res *Result) error {
	switch {
	case res.ClearPrev:
		m.clearPrev(agent)
	case len(res.Prev) > 0:
		if err := m.savePrev(agent, res.Prev); err != nil {
			return fmt.Errorf("配置已写入，但保存还原记录失败（移除时将无法还原被覆盖的原值）：%w", err)
		}
	}
	return nil
}

func applyOne(c Change) error {
	if c.Delete {
		return os.Remove(c.Path)
	}
	perm := os.FileMode(0o600)
	if st, err := os.Stat(c.Path); err == nil {
		perm = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	return atomicWrite(c.Path, c.After, perm)
}

func revertOne(c Change) error {
	if c.Existed {
		perm := os.FileMode(0o600)
		if st, err := os.Stat(c.Path); err == nil {
			perm = st.Mode().Perm()
		}
		return atomicWrite(c.Path, c.Before, perm)
	}
	return os.Remove(c.Path)
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".poolsvc.tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func safeName(p string) string {
	r := strings.NewReplacer(`\`, "_", "/", "_", ":", "_", " ", "_")
	return r.Replace(p)
}

func (m *Manager) backup(agent string, changes []Change) (string, error) {
	dir := filepath.Join(m.Dir, "agent-backups", agent, m.now().Format("20060102-150405.000"))
	type entry struct {
		Path    string `json:"path"`
		File    string `json:"file,omitempty"`
		Existed bool   `json:"existed"`
	}
	var manifest []entry
	need := false
	for _, c := range changes {
		e := entry{Path: c.Path, Existed: c.Existed}
		if c.Existed {
			need = true
			e.File = safeName(c.Path)
		}
		manifest = append(manifest, e)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for i, c := range changes {
		if c.Existed {
			if err := os.WriteFile(filepath.Join(dir, manifest[i].File), c.Before, 0o600); err != nil {
				return "", err
			}
		}
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		return "", err
	}
	if !need {
		// 全是新建文件：没有原内容可备份，但保留清单便于知道本次写了什么。
		return dir, nil
	}
	return dir, nil
}

func (m *Manager) prune(agent string) {
	root := filepath.Join(m.Dir, "agent-backups", agent)
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keepBackups {
		_ = os.RemoveAll(filepath.Join(root, names[0]))
		names = names[1:]
	}
}
