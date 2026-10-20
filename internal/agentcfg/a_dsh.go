package agentcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---- DeepSeek Harness ----

// dshAgent 写 DSH 的供应商配置。
//
// 写入位置由「档案布局」决定，而不是只看运行时版本号：
//   - ~/.dsh/profiles/<档案>/cordis.patch.yml：顶层是条目数组，供应商在 id 为 llm-pi-ai 的条目下；
//   - 没有任何档案时退回旧版 ~/.dsh/settings.yaml（llm-pi-ai.providers）。
//
// 密钥不写进供应商，而是通过 apiKeyEnv 指向 ~/.dsh/.credentials.yaml 里的一项引用。
type dshAgent struct{}

func (dshAgent) ID() string   { return "deepseek-harness" }
func (dshAgent) Name() string { return "DeepSeek Harness" }

const (
	dshEnvName      = "POOLSVC_DSH_API_KEY"
	dshPatchFile    = "cordis.patch.yml"
	dshSettingsFile = "settings.yaml"
	dshCredFile     = ".credentials.yaml"
	dshPluginID     = "llm-pi-ai"
	dshDefaultID    = "agent-default-model"
)

type dshTarget struct {
	home     string
	profiles []Choice
	profile  string
	legacy   bool
	file     string
	cred     string
}

func (dshAgent) home(env Env, dirOpt string) string {
	if dirOpt != "" {
		return env.path(dirOpt)
	}
	if v := env.env("DSH_HOME"); v != "" {
		return env.path(v)
	}
	return filepath.Join(env.Home, ".dsh")
}

func dshProfiles(home string) []string {
	ents, _ := os.ReadDir(filepath.Join(home, "profiles"))
	var out []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() || n == "node_modules" || strings.HasPrefix(n, ".") {
			continue
		}
		dir := filepath.Join(home, "profiles", n)
		if exists(filepath.Join(dir, dshPatchFile)) || exists(filepath.Join(dir, "package.json")) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// dshProfileState 看一个档案的补丁文件里有没有 llm-pi-ai 条目 / 我们的供应商。
func dshProfileState(patch string) (hasPlugin, hasOurs bool) {
	b, ok, err := readOpt(patch)
	if err != nil || !ok {
		return false, false
	}
	f, err := loadYAML(b, yaml.SequenceNode)
	if err != nil {
		return false, false
	}
	ent, _ := yseqFindByID(f.root, dshPluginID)
	if ent == nil {
		return false, false
	}
	cfg, _ := ymapGet(ent, "config")
	prov, _ := ymapGet(cfg, "providers")
	p, _ := ymapGet(prov, ProviderID)
	return true, p != nil
}

func (d dshAgent) target(env Env, dirOpt, profileOpt string) (dshTarget, error) {
	t := dshTarget{home: d.home(env, dirOpt)}
	t.cred = filepath.Join(t.home, dshCredFile)
	names := dshProfiles(t.home)
	if len(names) == 0 {
		t.legacy = true
		t.file = filepath.Join(t.home, dshSettingsFile)
		return t, nil
	}
	for _, n := range names {
		t.profiles = append(t.profiles, Choice{ID: n, Label: n})
	}
	pick := ""
	if profileOpt != "" {
		if !containsStr(names, profileOpt) {
			return t, fmt.Errorf("DSH 档案 %q 不存在，可选：%s", profileOpt, strings.Join(names, "、"))
		}
		pick = profileOpt
	}
	if pick == "" {
		for _, n := range names {
			if _, ours := dshProfileState(filepath.Join(t.home, "profiles", n, dshPatchFile)); ours {
				pick = n
				break
			}
		}
	}
	if pick == "" {
		for _, n := range names {
			if plug, _ := dshProfileState(filepath.Join(t.home, "profiles", n, dshPatchFile)); plug {
				pick = n
				break
			}
		}
	}
	if pick == "" {
		pick = names[0]
		if containsStr(names, "desktop") {
			pick = "desktop"
		}
	}
	t.profile = pick
	t.file = filepath.Join(t.home, "profiles", pick, dshPatchFile)
	return t, nil
}

func yseqFindByID(seq *yaml.Node, id string) (*yaml.Node, int) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil, -1
	}
	for i, it := range seq.Content {
		if v, _ := ymapGet(it, "id"); v != nil && v.Value == id {
			return it, i
		}
	}
	return nil, -1
}

func yscalarNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func ymapOf(pairs ...string) *yaml.Node {
	m := newYAMLKind(yaml.MappingNode)
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Content = append(m.Content, yscalarNode(pairs[i]), yscalarNode(pairs[i+1]))
	}
	return m
}

func dshEntry(in Input) Obj {
	var models []Obj
	for _, m := range in.Models {
		mo := Obj{{"id", m.ID}, {"name", displayName(m)}}
		if m.Context > 0 {
			mo = append(mo, KV{"contextWindow", m.Context})
		}
		if m.MaxOutput > 0 {
			mo = append(mo, KV{"maxTokens", m.MaxOutput})
		}
		mo = append(mo, KV{"input", inputModalities(m)})
		set := effortSet(m)
		var eff Obj
		for _, e := range ompEfforts {
			if set[e] {
				eff = append(eff, KV{e, e})
			}
		}
		if eff != nil {
			mo = append(mo, KV{"reasoningEfforts", eff})
		}
		models = append(models, mo)
	}
	return Obj{
		{"displayName", ProviderName},
		{"apiKeyEnv", dshEnvName},
		{"api", "openai-completions"},
		{"baseURL", in.OpenAIBase()},
		{"models", models},
	}
}

// providersOf 取出供应商映射。create 为 true 时按需创建中间节点。
func (t dshTarget) providersOf(f *yamlFile, create bool) (*yaml.Node, *yaml.Node, error) {
	if t.legacy {
		if !create {
			p, _ := ymapGet(f.root, dshPluginID)
			prov, _ := ymapGet(p, "providers")
			return prov, nil, nil
		}
		plug, err := yensureMap(f.root, dshPluginID)
		if err != nil {
			return nil, nil, err
		}
		prov, err := yensureMap(plug, "providers")
		return prov, nil, err
	}
	ent, _ := yseqFindByID(f.root, dshPluginID)
	if ent == nil {
		if !create {
			return nil, nil, nil
		}
		f.root.Style &^= yaml.FlowStyle
		ent = ymapOf("id", dshPluginID, "name", "@deepseek-ai/dsh-llm-pi-ai")
		f.root.Content = append(f.root.Content, ent)
	}
	if !create {
		cfg, _ := ymapGet(ent, "config")
		prov, _ := ymapGet(cfg, "providers")
		return prov, ent, nil
	}
	cfg, err := yensureMap(ent, "config")
	if err != nil {
		return nil, nil, err
	}
	prov, err := yensureMap(cfg, "providers")
	return prov, ent, err
}

func (t dshTarget) loadMain() (*yamlFile, []byte, bool, error) {
	b, ok, err := readOpt(t.file)
	if err != nil {
		return nil, nil, false, err
	}
	kind := yaml.SequenceNode
	if t.legacy {
		kind = yaml.MappingNode
	}
	f, err := loadYAML(b, kind)
	if err != nil {
		return nil, nil, false, fmt.Errorf("%s 无法解析，未做任何修改：%w", t.file, err)
	}
	return f, b, ok, nil
}

func dshOwned(prov *yaml.Node) (owned, present bool) {
	v, _ := ymapGet(prov, ProviderID)
	if v == nil {
		return false, false
	}
	env, _ := ymapGet(v, "apiKeyEnv")
	return strings.HasPrefix(yscalar(env), "POOLSVC_"), true
}

func (d dshAgent) Detect(env Env, opt Options, _ Prev) Info {
	info := Info{
		ID: d.ID(), Name: d.Name(), Protocol: "chat",
		Restart: "重启 DeepSeek Harness",
	}
	t, err := d.target(env, opt.Dir, opt.Profile)
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	info.Profiles, info.Profile = t.profiles, t.profile
	info.Files = fileInfos(t.file, t.cred)
	info.SupportsDefault = !t.legacy
	if isDir(t.home) {
		info.Evidence = append(info.Evidence, t.home)
	}
	info.Evidence = append(info.Evidence, env.lookPath("dsh")...)
	for _, root := range []string{env.AppData, env.LocalAppData} {
		if root != "" {
			if p := filepath.Join(root, "DeepSeek Harness"); isDir(p) {
				info.Evidence = append(info.Evidence, p)
			}
		}
	}
	info.Installed = len(info.Evidence) > 0
	if t.legacy {
		info.Notes = append(info.Notes, "没有发现档案目录，按旧版 settings.yaml 写入")
	}
	f, _, ok, err := t.loadMain()
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	if !ok {
		return info
	}
	prov, _, _ := t.providersOf(f, false)
	if owned, present := dshOwned(prov); present && owned {
		info.Imported = true
		v, _ := ymapGet(prov, ProviderID)
		models, _ := ymapGet(v, "models")
		if models != nil && models.Kind == yaml.SequenceNode {
			for _, m := range models.Content {
				id, _ := ymapGet(m, "id")
				info.ImportedModels = append(info.ImportedModels, yscalar(id))
			}
		}
	}
	return info
}

// credentialsChange 在凭据文件里写入 / 删除我们的密钥引用。
func (t dshTarget) credentialsChange(key string, remove bool) (*Change, error) {
	b, existed, err := readOpt(t.cred)
	if err != nil {
		return nil, err
	}
	if remove && !existed {
		return nil, nil
	}
	f, err := loadYAML(b, yaml.MappingNode)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", t.cred, err)
	}
	store := f.root
	ver, _ := ymapGet(f.root, "version")
	switch {
	case ver != nil && (ver.Value == "1" || ver.Value == "1.0"):
		if remove {
			if r, _ := ymapGet(f.root, "refs"); r != nil && r.Kind == yaml.MappingNode {
				store = r
			} else {
				return nil, nil
			}
		} else {
			store, err = yensureMap(f.root, "refs")
			if err != nil {
				return nil, err
			}
		}
	case ver != nil:
		return nil, fmt.Errorf("%s 的 version 不是 1，无法识别其布局，未做任何修改", t.cred)
	case !existed:
		ymapSet(f.root, "version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
		store, err = yensureMap(f.root, "refs")
		if err != nil {
			return nil, err
		}
	}
	if remove {
		if !ymapDel(store, dshEnvName) {
			return nil, nil
		}
	} else {
		ymapSet(store, dshEnvName, yscalarNode(key))
	}
	after, err := f.bytes()
	if err != nil {
		return nil, err
	}
	return &Change{Path: t.cred, Before: b, Existed: existed, After: after}, nil
}

func (d dshAgent) Build(env Env, in Input, prev Prev) (*Result, error) {
	if len(in.Models) == 0 {
		return nil, errors.New("请至少选择一个模型")
	}
	t, err := d.target(env, in.Dir, in.Profile)
	if err != nil {
		return nil, err
	}
	f, before, existed, err := t.loadMain()
	if err != nil {
		return nil, err
	}
	prov, _, err := t.providersOf(f, true)
	if err != nil {
		return nil, err
	}
	if owned, present := dshOwned(prov); present && !owned {
		return nil, fmt.Errorf("%s 里已经有名为 %s 的供应商，且不是本工具写入的；为避免覆盖，请先改名或删除它", t.file, ProviderID)
	}
	entry := dshEntry(in)
	node, err := yamlNode(entry)
	if err != nil {
		return nil, err
	}
	ymapSet(prov, ProviderID, node)

	p := copyPrev(prev)
	res := &Result{}
	var defSnippet string
	if in.Default != "" && !t.legacy {
		if err := d.setDefault(f, p, in.Default); err != nil {
			return nil, err
		}
		defSnippet = yamlText([]Obj{{{"id", dshDefaultID}, {"config", Obj{{"provider", ProviderID}, {"model", in.Default}}}}}) + "\n\n"
	}
	after, err := f.bytes()
	if err != nil {
		return nil, err
	}
	kind := yaml.SequenceNode
	if t.legacy {
		kind = yaml.MappingNode
	}
	if _, err := loadYAML(after, kind); err != nil {
		return nil, fmt.Errorf("内部校验失败：写入后的 YAML 无法解析，已放弃：%w", err)
	}
	for _, m := range in.Models {
		res.Imported = append(res.Imported, m.ID)
	}
	var snippet string
	if t.legacy {
		snippet = yamlText(Obj{{dshPluginID, Obj{{"providers", Obj{{ProviderID, entry}}}}}})
	} else {
		snippet = yamlText([]Obj{{{"id", dshPluginID}, {"config", Obj{{"providers", Obj{{ProviderID, entry}}}}}}})
	}
	res.Changes = []Change{{
		Path: t.file, Before: before, Existed: existed, After: after,
		Snippet: defSnippet + snippet, Lang: "yaml", Secrets: []string{in.APIKey},
	}}
	cc, err := t.credentialsChange(in.APIKey, false)
	if err != nil {
		return nil, err
	}
	cc.Snippet = "refs:\n  " + dshEnvName + ": " + in.APIKey
	cc.Lang = "yaml"
	cc.Secrets = []string{in.APIKey}
	res.Changes = append(res.Changes, *cc)
	if in.Default == "" && !t.legacy {
		res.Warnings = append(res.Warnings, "未设为默认模型：供应商已写入，请在 DeepSeek Harness 的模型选择里切换到 PoolService")
	}
	res.Prev = p
	return res, nil
}

// setDefault 把 agent-default-model 条目指向我们的供应商，改之前记下原值。
func (dshAgent) setDefault(f *yamlFile, p Prev, model string) error {
	ent, _ := yseqFindByID(f.root, dshDefaultID)
	if _, ok := p["dsh.entry"]; !ok {
		p["dsh.entry"] = PrevVal{Present: ent != nil}
	}
	if ent == nil {
		f.root.Style &^= yaml.FlowStyle
		ent = ymapOf("id", dshDefaultID, "name", "@deepseek-ai/dsh-agent-default-model")
		f.root.Content = append(f.root.Content, ent)
	}
	cfg, err := yensureMap(ent, "config")
	if err != nil {
		return err
	}
	for _, kv := range [][2]string{{"provider", ProviderID}, {"model", model}} {
		key := "dsh." + kv[0]
		if _, ok := p[key]; !ok {
			old, _ := ymapGet(cfg, kv[0])
			pv := PrevVal{Present: old != nil}
			if old != nil {
				pv.Raw, _ = json.Marshal(old.Value)
			}
			p[key] = pv
		}
		ymapSet(cfg, kv[0], yscalarNode(kv[1]))
	}
	return nil
}

func (d dshAgent) Remove(env Env, opt Options, prev Prev) (*Result, error) {
	t, err := d.target(env, opt.Dir, opt.Profile)
	if err != nil {
		return nil, err
	}
	b, existed, err := readOpt(t.file)
	if err != nil {
		return nil, err
	}
	if !existed {
		return nil, fmt.Errorf("没有找到 %s，无需移除", t.file)
	}
	f, _, _, err := t.loadMain()
	if err != nil {
		return nil, err
	}
	prov, ent, _ := t.providersOf(f, false)
	if owned, present := dshOwned(prov); !present || !owned {
		return nil, errors.New("没有发现本工具写入的 DeepSeek Harness 配置，无需移除")
	}
	ymapDel(prov, ProviderID)
	if prov.Kind == yaml.MappingNode && len(prov.Content) == 0 {
		if t.legacy {
			if plug, _ := ymapGet(f.root, dshPluginID); plug != nil {
				ymapDel(plug, "providers")
				if len(plug.Content) == 0 {
					ymapDel(f.root, dshPluginID)
				}
			}
		} else if ent != nil {
			if cfg, _ := ymapGet(ent, "config"); cfg != nil {
				ymapDel(cfg, "providers")
				if len(cfg.Content) == 0 {
					ymapDel(ent, "config")
				}
			}
		}
	}
	if !t.legacy {
		d.restoreDefault(f, prev)
	}
	after, err := f.bytes()
	if err != nil {
		return nil, err
	}
	res := &Result{ClearPrev: true, Changes: []Change{{Path: t.file, Before: b, Existed: true, After: after}}}
	if cc, err := t.credentialsChange("", true); err != nil {
		res.Warnings = append(res.Warnings, "供应商已移除，但清理密钥引用失败："+err.Error())
	} else if cc != nil {
		res.Changes = append(res.Changes, *cc)
	}
	return res, nil
}

func (dshAgent) restoreDefault(f *yamlFile, prev Prev) {
	ent, idx := yseqFindByID(f.root, dshDefaultID)
	if ent == nil {
		return
	}
	cfg, _ := ymapGet(ent, "config")
	if cfg == nil {
		return
	}
	cur, _ := ymapGet(cfg, "provider")
	if yscalar(cur) != ProviderID {
		return // 用户之后又改过默认模型，不动
	}
	if pv, ok := prev["dsh.entry"]; ok && !pv.Present {
		f.root.Content = append(f.root.Content[:idx], f.root.Content[idx+1:]...)
		return
	}
	for _, k := range []string{"provider", "model"} {
		pv, ok := prev["dsh."+k]
		switch {
		case ok && pv.Present:
			var old string
			if json.Unmarshal(pv.Raw, &old) == nil {
				ymapSet(cfg, k, yscalarNode(old))
			}
		default:
			ymapDel(cfg, k)
		}
	}
}
