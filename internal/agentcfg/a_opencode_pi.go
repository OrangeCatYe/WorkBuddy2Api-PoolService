package agentcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ---- OpenCode ----

// openCode 写 opencode.json(c) 的 provider.poolservice（OpenAI 兼容适配器 @ai-sdk/openai-compatible）。
type openCode struct{}

func (openCode) ID() string   { return "opencode" }
func (openCode) Name() string { return "OpenCode" }

// file 解析配置文件：精确文件（OPENCODE_CONFIG）> 目录 > 默认目录。
// 目录里 opencode.json 优先，只有 opencode.jsonc 时用它，都没有则新建 opencode.json。
func (openCode) file(env Env, dirOpt string) string {
	pick := func(d string) string {
		for _, n := range []string{"opencode.json", "opencode.jsonc"} {
			if p := filepath.Join(d, n); exists(p) {
				return p
			}
		}
		return filepath.Join(d, "opencode.json")
	}
	if dirOpt != "" {
		return pick(env.path(dirOpt))
	}
	if v := env.env("OPENCODE_CONFIG"); v != "" {
		return env.path(v)
	}
	if x := env.env("XDG_CONFIG_HOME"); x != "" {
		return pick(filepath.Join(env.path(x), "opencode"))
	}
	return pick(filepath.Join(env.Home, ".config", "opencode"))
}

func (o openCode) entry(in Input) Obj {
	models := Obj{}
	for _, m := range in.Models {
		mo := Obj{{"name", displayName(m)}}
		if m.Context > 0 {
			out := m.MaxOutput
			if out <= 0 || out > m.Context {
				out = m.Context
				if out > 32000 {
					out = 32000
				}
			}
			mo = append(mo, KV{"limit", Obj{{"context", m.Context}, {"output", out}}})
		}
		if m.Reasoning {
			mo = append(mo, KV{"reasoning", true})
		}
		if m.Tools {
			mo = append(mo, KV{"tool_call", true})
		}
		if m.Vision {
			mo = append(mo, KV{"attachment", true}, KV{"modalities", Obj{{"input", []string{"text", "image"}}, {"output", []string{"text"}}}})
		}
		models = append(models, KV{m.ID, mo})
	}
	return Obj{
		{"npm", "@ai-sdk/openai-compatible"},
		{"name", ProviderName},
		{"options", Obj{{"baseURL", in.OpenAIBase()}, {"apiKey", in.APIKey}}},
		{"models", models},
	}
}

func openCodeCheck(ed *jsonEditor, file string) error {
	if ed.Kind() != '{' {
		return fmt.Errorf("%s 的根节点不是对象", file)
	}
	if k := ed.Kind("provider"); k != 0 && k != '{' {
		return fmt.Errorf("%s 中的 provider 不是对象", file)
	}
	if ed.Kind("provider") == 0 && ed.Kind("providers") == '{' {
		return fmt.Errorf("%s 使用了新版的 providers 结构，本工具暂时只支持 provider 结构，未做任何修改", file)
	}
	return nil
}

func (o openCode) Detect(env Env, opt Options, _ Prev) Info {
	file := o.file(env, opt.Dir)
	info := Info{
		ID: o.ID(), Name: o.Name(), Protocol: "chat",
		Files: fileInfos(file), SupportsDefault: true,
		Restart: "重启 OpenCode",
	}
	if d := filepath.Dir(file); isDir(d) {
		info.Evidence = append(info.Evidence, d)
	}
	info.Evidence = append(info.Evidence, env.lookPath("opencode")...)
	for _, root := range []string{env.AppData, env.LocalAppData} {
		if root != "" {
			if p := filepath.Join(root, "ai.opencode.desktop"); isDir(p) {
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
	if err == nil {
		err = openCodeCheck(ed, file)
	}
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	if ed.Kind("provider", ProviderID) == '{' {
		info.Imported = true
		info.ImportedModels = ed.Keys("provider", ProviderID, "models")
	}
	return info
}

func (o openCode) Build(env Env, in Input, prev Prev) (*Result, error) {
	if len(in.Models) == 0 {
		return nil, errors.New("请至少选择一个模型")
	}
	file := o.file(env, in.Dir)
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	ed, err := newJSONEditor(before)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", file, err)
	}
	if err := openCodeCheck(ed, file); err != nil {
		return nil, err
	}
	if ed.fresh {
		if err := ed.Set("https://opencode.ai/config.json", "$schema"); err != nil {
			return nil, err
		}
	}
	if ed.Kind("provider", ProviderID) == '{' {
		var name string
		_, _ = ed.Get(&name, "provider", ProviderID, "name")
		if name != ProviderName {
			return nil, fmt.Errorf("%s 里已经有名为 %s 的自定义供应商，且不是本工具写入的；为避免覆盖，请先改名或删除它", file, ProviderID)
		}
	}
	entry := o.entry(in)
	if err := ed.Set(entry, "provider", ProviderID); err != nil {
		return nil, err
	}
	p := copyPrev(prev)
	res := &Result{}
	for _, m := range in.Models {
		res.Imported = append(res.Imported, m.ID)
	}
	if in.Default != "" {
		if err := setWithPrev(ed, p, "model", ProviderID+"/"+in.Default, "model"); err != nil {
			return nil, err
		}
	}
	raw, _ := json.MarshalIndent(Obj{{"provider", Obj{{ProviderID, entry}}}}, "", "  ")
	res.Prev = p
	res.Changes = []Change{{
		Path: file, Before: before, Existed: existed, After: ed.Bytes(),
		Snippet: string(raw), Lang: "json", Secrets: []string{in.APIKey},
	}}
	return res, nil
}

func (o openCode) Remove(env Env, opt Options, prev Prev) (*Result, error) {
	file := o.file(env, opt.Dir)
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
	if ed.Kind("provider", ProviderID) != '{' {
		return nil, errors.New("没有发现本工具写入的 OpenCode 配置，无需移除")
	}
	if err := ed.Delete("provider", ProviderID); err != nil {
		return nil, err
	}
	if ed.Kind("provider") == '{' && len(ed.Keys("provider")) == 0 {
		if err := ed.Delete("provider"); err != nil {
			return nil, err
		}
	}
	var cur string
	_, _ = ed.Get(&cur, "model")
	if strings.HasPrefix(cur, ProviderID+"/") {
		if ok, err := restorePrev(ed, prev, "model", "model"); err != nil {
			return nil, err
		} else if !ok {
			if err := ed.Delete("model"); err != nil {
				return nil, err
			}
		}
	}
	return &Result{
		ClearPrev: true,
		Changes:   []Change{{Path: file, Before: before, Existed: true, After: ed.Bytes()}},
	}, nil
}

// ---- Pi Agent ----

// piAgent 写 ~/.pi/agent/models.json 的 providers.poolservice。
type piAgent struct{}

func (piAgent) ID() string   { return "pi" }
func (piAgent) Name() string { return "Pi Agent" }

func (piAgent) dir(env Env, dirOpt string) string {
	if dirOpt != "" {
		return env.path(dirOpt)
	}
	if v := env.env("PI_CODING_AGENT_DIR"); v != "" {
		return env.path(v)
	}
	return filepath.Join(env.Home, ".pi", "agent")
}

func (p piAgent) file(env Env, dirOpt string) string {
	return filepath.Join(p.dir(env, dirOpt), "models.json")
}

// piThinkingLevelMap 把模型支持的思考档位映射到 Pi 的 off..max 七档，不支持的档位为 null。
func piThinkingLevelMap(efforts []string) Obj {
	if len(efforts) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, e := range efforts {
		set[strings.ToLower(strings.TrimSpace(e))] = true
	}
	out := Obj{}
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		up := level
		if level == "off" {
			up = "none"
		}
		if set[up] {
			out = append(out, KV{level, up})
		} else {
			out = append(out, KV{level, nil})
		}
	}
	return out
}

func (p piAgent) entry(in Input) Obj {
	var models []Obj
	for _, m := range in.Models {
		mo := Obj{
			{"id", m.ID},
			{"name", displayName(m)},
			{"reasoning", m.Reasoning},
			{"input", inputModalities(m)},
		}
		if m.Context > 0 {
			mo = append(mo, KV{"contextWindow", m.Context})
		}
		if m.MaxOutput > 0 {
			mo = append(mo, KV{"maxTokens", m.MaxOutput})
		}
		if tl := piThinkingLevelMap(m.Efforts); tl != nil {
			mo = append(mo, KV{"thinkingLevelMap", tl})
		}
		models = append(models, mo)
	}
	return Obj{
		{"name", ProviderName},
		{"baseUrl", in.OpenAIBase()},
		{"api", "openai-completions"},
		{"apiKey", in.APIKey},
		{"authHeader", true},
		{"compat", Obj{{"supportsDeveloperRole", false}, {"supportsStore", false}, {"maxTokensField", "max_tokens"}}},
		{"models", models},
	}
}

func piCheck(ed *jsonEditor, file string) error {
	if ed.Kind() != '{' {
		return fmt.Errorf("%s 的根节点不是对象", file)
	}
	if k := ed.Kind("providers"); k != 0 && k != '{' {
		return fmt.Errorf("%s 中的 providers 不是对象", file)
	}
	return nil
}

func (p piAgent) Detect(env Env, opt Options, _ Prev) Info {
	file := p.file(env, opt.Dir)
	info := Info{
		ID: p.ID(), Name: p.Name(), Protocol: "chat",
		Files:   fileInfos(file),
		Restart: "重启 pi；已打开的会话里可用 /model 切换",
		Notes:   []string{"Pi 在会话内用 /model 选择模型，这里只登记模型"},
	}
	if d := p.dir(env, opt.Dir); isDir(d) {
		info.Evidence = append(info.Evidence, d)
	}
	info.Evidence = append(info.Evidence, env.lookPath("pi")...)
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
	if err == nil {
		err = piCheck(ed, file)
	}
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	if ed.Kind("providers", ProviderID) == '{' {
		info.Imported = true
		for i := 0; i < ed.Len("providers", ProviderID, "models"); i++ {
			var m struct {
				ID string `json:"id"`
			}
			if ed.Elem(&m, i, "providers", ProviderID, "models") == nil {
				info.ImportedModels = append(info.ImportedModels, m.ID)
			}
		}
	}
	return info
}

func (p piAgent) Build(env Env, in Input, _ Prev) (*Result, error) {
	if len(in.Models) == 0 {
		return nil, errors.New("请至少选择一个模型")
	}
	file := p.file(env, in.Dir)
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	ed, err := newJSONEditor(before)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", file, err)
	}
	if err := piCheck(ed, file); err != nil {
		return nil, err
	}
	if ed.Kind("providers", ProviderID) == '{' {
		var name string
		_, _ = ed.Get(&name, "providers", ProviderID, "name")
		if name != ProviderName {
			return nil, fmt.Errorf("%s 里已经有名为 %s 的供应商，且不是本工具写入的；为避免覆盖，请先改名或删除它", file, ProviderID)
		}
	}
	entry := p.entry(in)
	if err := ed.Set(entry, "providers", ProviderID); err != nil {
		return nil, err
	}
	res := &Result{}
	for _, m := range in.Models {
		res.Imported = append(res.Imported, m.ID)
	}
	raw, _ := json.MarshalIndent(Obj{{"providers", Obj{{ProviderID, entry}}}}, "", "  ")
	res.Changes = []Change{{
		Path: file, Before: before, Existed: existed, After: ed.Bytes(),
		Snippet: string(raw), Lang: "json", Secrets: []string{in.APIKey},
	}}
	return res, nil
}

func (p piAgent) Remove(env Env, opt Options, _ Prev) (*Result, error) {
	file := p.file(env, opt.Dir)
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
	if ed.Kind("providers", ProviderID) != '{' {
		return nil, errors.New("没有发现本工具写入的 Pi 配置，无需移除")
	}
	if err := ed.Delete("providers", ProviderID); err != nil {
		return nil, err
	}
	if ed.Kind("providers") == '{' && len(ed.Keys("providers")) == 0 {
		if err := ed.Delete("providers"); err != nil {
			return nil, err
		}
	}
	return &Result{Changes: []Change{{Path: file, Before: before, Existed: true, After: ed.Bytes()}}}, nil
}
