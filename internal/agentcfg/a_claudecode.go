package agentcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func copyPrev(p Prev) Prev {
	out := Prev{}
	for k, v := range p {
		out[k] = v
	}
	return out
}

// setWithPrev 写入路径处的值；若还没有该键的还原记录，先把原值记下来。
func setWithPrev(ed *jsonEditor, prev Prev, key string, v any, path ...string) error {
	if _, ok := prev[key]; !ok {
		var raw json.RawMessage
		present, err := ed.Get(&raw, path...)
		if err != nil {
			return err
		}
		pv := PrevVal{Present: present}
		if present {
			pv.Raw = append(json.RawMessage(nil), raw...)
		}
		prev[key] = pv
	}
	return ed.Set(v, path...)
}

// restorePrev 把路径处还原成记录的原值；原先不存在则删除。没有记录返回 false。
func restorePrev(ed *jsonEditor, prev Prev, key string, path ...string) (bool, error) {
	pv, ok := prev[key]
	if !ok {
		return false, nil
	}
	if pv.Present {
		return true, ed.Set(pv.Raw, path...)
	}
	return true, ed.Delete(path...)
}

// ---- Claude Code ----

// claudeCode 通过 settings.json 的 env 段切换接口地址、密钥与模型。
//
// Claude Code 没有「多供应商」的概念，env 是全局生效的，所以这是「接管」：
// 写入前把被覆盖的原值记下来，移除时还原。
type claudeCode struct{}

func (claudeCode) ID() string   { return "claude-code" }
func (claudeCode) Name() string { return "Claude Code" }

var ccEnvKeys = struct{ Base, Token, Model, Fast, APIKey string }{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "ANTHROPIC_API_KEY",
}

func (claudeCode) dir(env Env, dirOpt string) string {
	if dirOpt != "" {
		return env.path(dirOpt)
	}
	if v := env.env("CLAUDE_CONFIG_DIR"); v != "" {
		return env.path(v)
	}
	return filepath.Join(env.Home, ".claude")
}

func (c claudeCode) file(env Env, dirOpt string) string {
	d := c.dir(env, dirOpt)
	s := filepath.Join(d, "settings.json")
	if !exists(s) {
		if legacy := filepath.Join(d, "claude.json"); exists(legacy) {
			return legacy
		}
	}
	return s
}

func (c claudeCode) Detect(env Env, opt Options, prev Prev) Info {
	file := c.file(env, opt.Dir)
	info := Info{
		ID: c.ID(), Name: c.Name(), Protocol: "anthropic",
		Files:           fileInfos(file),
		SupportsDefault: true, NeedsDefault: true, SupportsFast: true, Takeover: true,
		Restart: "新开的 claude 会话生效，已打开的会话需要重启",
		Notes:   []string{"Claude Code 的接口地址是全局设置：写入后所有会话都会改走本网关；移除时会还原原来的值"},
	}
	if d := c.dir(env, opt.Dir); isDir(d) {
		info.Evidence = append(info.Evidence, d)
	}
	info.Evidence = append(info.Evidence, env.lookPath("claude")...)
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
	var token, model string
	_, _ = ed.Get(&token, "env", ccEnvKeys.Token)
	_, _ = ed.Get(&model, "env", ccEnvKeys.Model)
	if _, hasPrev := prev["env."+ccEnvKeys.Token]; strings.HasPrefix(token, "sk-pool-") || (hasPrev && token != "") {
		info.Imported = true
		if model != "" {
			info.ImportedModels = []string{model}
		}
	}
	return info
}

func (c claudeCode) Build(env Env, in Input, prev Prev) (*Result, error) {
	if strings.TrimSpace(in.Default) == "" {
		return nil, errors.New("请选择 Claude Code 使用的主模型")
	}
	file := c.file(env, in.Dir)
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	ed, err := newJSONEditor(before)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", file, err)
	}
	if ed.Kind() != '{' {
		return nil, fmt.Errorf("%s 的根节点不是对象", file)
	}
	switch ed.Kind("env") {
	case 0:
		if err := ed.Set(Obj{}, "env"); err != nil {
			return nil, err
		}
	case '{':
	default:
		return nil, fmt.Errorf("%s 中的 env 不是对象", file)
	}
	p := copyPrev(prev)
	res := &Result{}
	var hadAPIKey bool
	if present, _ := ed.Get(new(json.RawMessage), "env", ccEnvKeys.APIKey); present {
		hadAPIKey = true
	}

	set := func(k string, v string) error {
		return setWithPrev(ed, p, "env."+k, v, "env", k)
	}
	if err := set(ccEnvKeys.Base, in.AnthropicBase()); err != nil {
		return nil, err
	}
	if err := set(ccEnvKeys.Token, in.APIKey); err != nil {
		return nil, err
	}
	if err := set(ccEnvKeys.Model, in.Default); err != nil {
		return nil, err
	}
	snippetEnv := Obj{{ccEnvKeys.Base, in.AnthropicBase()}, {ccEnvKeys.Token, in.APIKey}, {ccEnvKeys.Model, in.Default}}
	res.Imported = []string{in.Default}
	if in.Fast != "" {
		if err := set(ccEnvKeys.Fast, in.Fast); err != nil {
			return nil, err
		}
		snippetEnv = append(snippetEnv, KV{ccEnvKeys.Fast, in.Fast})
		if in.Fast != in.Default {
			res.Imported = append(res.Imported, in.Fast)
		}
	} else if _, tracked := p["env."+ccEnvKeys.Fast]; tracked {
		// 之前由本工具写过快速模型，这次不要了：还原成原值。
		if _, err := restorePrev(ed, p, "env."+ccEnvKeys.Fast, "env", ccEnvKeys.Fast); err != nil {
			return nil, err
		}
		delete(p, "env."+ccEnvKeys.Fast)
	}
	if hadAPIKey {
		res.Warnings = append(res.Warnings, "env 中已有 ANTHROPIC_API_KEY，Claude Code 可能优先使用它而不是本网关的密钥；如果请求没有走到本网关，请手动删除这一项")
	}
	if ed.Kind("apiKeyHelper") != 0 {
		res.Warnings = append(res.Warnings, "settings.json 里配置了 apiKeyHelper，它会覆盖这里写入的密钥，请确认是否仍需要")
	}
	raw, _ := json.MarshalIndent(Obj{{"env", snippetEnv}}, "", "  ")
	res.Prev = p
	res.Changes = []Change{{
		Path: file, Before: before, Existed: existed, After: ed.Bytes(),
		Snippet: string(raw), Lang: "json", Secrets: []string{in.APIKey},
	}}
	return res, nil
}

func (c claudeCode) Remove(env Env, opt Options, prev Prev) (*Result, error) {
	file := c.file(env, opt.Dir)
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
	var token string
	_, _ = ed.Get(&token, "env", ccEnvKeys.Token)
	_, tracked := prev["env."+ccEnvKeys.Token]
	if !tracked && !strings.HasPrefix(token, "sk-pool-") {
		return nil, errors.New("没有发现本工具写入的 Claude Code 配置，无需移除")
	}
	for _, k := range []string{ccEnvKeys.Base, ccEnvKeys.Token, ccEnvKeys.Model, ccEnvKeys.Fast} {
		restored, err := restorePrev(ed, prev, "env."+k, "env", k)
		if err != nil {
			return nil, err
		}
		// 没有还原记录（记录文件丢失）时，只删除确定是我们写的三项。
		if !restored && k != ccEnvKeys.Fast {
			if err := ed.Delete("env", k); err != nil {
				return nil, err
			}
		}
	}
	if ed.Kind("env") == '{' && len(ed.Keys("env")) == 0 {
		if err := ed.Delete("env"); err != nil {
			return nil, err
		}
	}
	return &Result{
		ClearPrev: true,
		Changes:   []Change{{Path: file, Before: before, Existed: true, After: ed.Bytes()}},
	}, nil
}
