package agentcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// 受管标记：models.json 的条目没有「供应商」概念，用 vendor 字段标记归属。
const vendorMark = "PoolService"

// modelsJSON WorkBuddy / CodeBuddy：两者读同一种 models.json（OpenAI Chat 协议，平铺的模型列表）。
type modelsJSON struct {
	id, name, dirName string
	cmds, appDirs     []string
}

func (a modelsJSON) ID() string   { return a.id }
func (a modelsJSON) Name() string { return a.name }

func (a modelsJSON) dir(env Env, dirOpt string) string {
	if dirOpt != "" {
		return env.path(dirOpt)
	}
	return filepath.Join(env.Home, a.dirName)
}

func (a modelsJSON) file(env Env, dirOpt string) string {
	return filepath.Join(a.dir(env, dirOpt), "models.json")
}

// modelsArrayPath 找到模型数组：根是数组时就是根，根是对象时在 models 键下。
func modelsArrayPath(ed *jsonEditor) ([]string, error) {
	switch ed.Kind() {
	case '[':
		return nil, nil
	case '{':
		if k := ed.Kind("models"); k != 0 && k != '[' {
			return nil, errors.New("models 不是数组")
		}
		return []string{"models"}, nil
	}
	return nil, errors.New("根节点既不是对象也不是数组")
}

type jsonEntry struct {
	idx   int
	id    string
	owned bool
}

func scanEntries(ed *jsonEditor, path []string) []jsonEntry {
	n := ed.Len(path...)
	out := make([]jsonEntry, 0, n)
	for i := 0; i < n; i++ {
		var e struct {
			ID     string `json:"id"`
			Vendor string `json:"vendor"`
		}
		if err := ed.Elem(&e, i, path...); err != nil {
			continue
		}
		out = append(out, jsonEntry{idx: i, id: e.ID, owned: e.Vendor == vendorMark})
	}
	return out
}

func (a modelsJSON) entry(in Input, m Model) Obj {
	o := Obj{
		{"id", m.ID},
		{"name", displayName(m)},
		{"vendor", vendorMark},
		{"url", in.OpenAIBase() + "/chat/completions"},
		{"apiKey", in.APIKey},
	}
	if m.Context > 0 {
		o = append(o, KV{"maxInputTokens", m.Context})
	}
	if m.MaxOutput > 0 {
		o = append(o, KV{"maxOutputTokens", m.MaxOutput})
	}
	o = append(o, KV{"supportsToolCall", m.Tools}, KV{"supportsImages", m.Vision})
	if m.Reasoning {
		o = append(o, KV{"supportsReasoning", true})
	}
	return o
}

func (a modelsJSON) Detect(env Env, opt Options, _ Prev) Info {
	file := a.file(env, opt.Dir)
	info := Info{
		ID: a.id, Name: a.name, Protocol: "chat",
		Files:   fileInfos(file),
		Restart: "完全退出 " + a.name + "（包括托盘图标）后重新打开；修改 models.json 后部分版本会自动热加载",
		Notes:   []string{"模型地址写入完整的 /chat/completions 路径，这是官方文档要求的写法"},
	}
	if d := a.dir(env, opt.Dir); isDir(d) {
		info.Evidence = append(info.Evidence, d)
	}
	info.Evidence = append(info.Evidence, env.lookPath(a.cmds...)...)
	for _, d := range a.appDirs {
		for _, root := range []string{env.AppData, env.LocalAppData, filepath.Join(env.LocalAppData, "Programs")} {
			if root == "" || (root == filepath.Join("", "Programs")) {
				continue
			}
			if p := filepath.Join(root, d); isDir(p) {
				info.Evidence = append(info.Evidence, p)
			}
		}
	}
	info.Installed = len(info.Evidence) > 0
	b, ok, err := readOpt(file)
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	if !ok {
		return info
	}
	ed, err := newJSONEditor(b)
	if err != nil {
		info.Blocked = fmt.Sprintf("%s 无法解析：%v", file, err)
		return info
	}
	path, err := modelsArrayPath(ed)
	if err != nil {
		info.Blocked = fmt.Sprintf("%s 结构不符合预期：%v", file, err)
		return info
	}
	for _, e := range scanEntries(ed, path) {
		if e.owned {
			info.ImportedModels = append(info.ImportedModels, e.id)
		}
	}
	info.Imported = len(info.ImportedModels) > 0
	return info
}

func (a modelsJSON) Build(env Env, in Input, _ Prev) (*Result, error) {
	if len(in.Models) == 0 {
		return nil, errors.New("请至少选择一个模型")
	}
	file := a.file(env, in.Dir)
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	ed, err := newJSONEditor(before)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", file, err)
	}
	path, err := modelsArrayPath(ed)
	if err != nil {
		return nil, fmt.Errorf("%s 结构不符合预期：%w", file, err)
	}
	if ed.Kind(path...) == 0 {
		if err := ed.Set([]any{}, path...); err != nil {
			return nil, err
		}
	}
	want := map[string]bool{}
	for _, m := range in.Models {
		want[m.ID] = true
	}
	res := &Result{}

	// 先删掉本工具写过、但这次不再选择的条目。
	var removed []string
	cur := scanEntries(ed, path)
	for i := len(cur) - 1; i >= 0; i-- {
		if cur[i].owned && !want[cur[i].id] {
			if err := ed.ArrayDelete(cur[i].idx, path...); err != nil {
				return nil, err
			}
			removed = append(removed, cur[i].id)
		}
	}

	byID := map[string]jsonEntry{}
	for _, e := range scanEntries(ed, path) {
		if _, dup := byID[e.id]; !dup {
			byID[e.id] = e
		}
	}
	var snippet []Obj
	for _, m := range in.Models {
		ent := a.entry(in, m)
		if cur, ok := byID[m.ID]; ok {
			if !cur.owned {
				res.Skipped = append(res.Skipped, Skip{m.ID, "已有同名的自定义模型，为避免覆盖你的配置已跳过"})
				continue
			}
			if err := ed.ArraySet(ent, cur.idx, path...); err != nil {
				return nil, err
			}
		} else if err := ed.ArrayAppend(ent, path...); err != nil {
			return nil, err
		}
		res.Imported = append(res.Imported, m.ID)
		snippet = append(snippet, ent)
	}

	if len(path) > 0 && ed.Kind("availableModels") == '[' {
		var add []string
		for _, id := range res.Imported {
			add = append(add, id)
		}
		if err := syncStringArray(ed, removed, add, "availableModels"); err != nil {
			return nil, err
		}
	}
	raw, _ := json.MarshalIndent(snippet, "", "  ")
	res.Changes = []Change{{
		Path: file, Before: before, Existed: existed, After: ed.Bytes(),
		Snippet: string(raw), Lang: "json", Secrets: []string{in.APIKey},
	}}
	if len(res.Imported) == 0 {
		return nil, errors.New("所选模型都与已有的自定义模型同名，没有可写入的内容")
	}
	return res, nil
}

func (a modelsJSON) Remove(env Env, opt Options, _ Prev) (*Result, error) {
	file := a.file(env, opt.Dir)
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	if !existed {
		return nil, fmt.Errorf("没有找到 %s，无需移除", file)
	}
	ed, err := newJSONEditor(before)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", file, err)
	}
	path, err := modelsArrayPath(ed)
	if err != nil {
		return nil, err
	}
	var removed []string
	cur := scanEntries(ed, path)
	for i := len(cur) - 1; i >= 0; i-- {
		if cur[i].owned {
			if err := ed.ArrayDelete(cur[i].idx, path...); err != nil {
				return nil, err
			}
			removed = append(removed, cur[i].id)
		}
	}
	if len(removed) == 0 {
		return nil, errors.New("没有发现本工具写入的模型，无需移除")
	}
	if len(path) > 0 && ed.Kind("availableModels") == '[' {
		if err := syncStringArray(ed, removed, nil, "availableModels"); err != nil {
			return nil, err
		}
	}
	return &Result{
		Imported: removed,
		Changes:  []Change{{Path: file, Before: before, Existed: true, After: ed.Bytes()}},
	}, nil
}

// syncStringArray 在字符串数组里删去 remove 中的项、追加 add 中尚未存在的项，逐项编辑以保留原有排版。
func syncStringArray(ed *jsonEditor, remove, add []string, path ...string) error {
	n := ed.Len(path...)
	cur := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var s string
		if err := ed.Elem(&s, i, path...); err != nil {
			s = "\x00non-string"
		}
		cur = append(cur, s)
	}
	for i := n - 1; i >= 0; i-- {
		if containsStr(remove, cur[i]) {
			if err := ed.ArrayDelete(i, path...); err != nil {
				return err
			}
			cur = append(cur[:i], cur[i+1:]...)
		}
	}
	for _, id := range add {
		if !containsStr(cur, id) {
			if err := ed.ArrayAppend(id, path...); err != nil {
				return err
			}
			cur = append(cur, id)
		}
	}
	return nil
}
