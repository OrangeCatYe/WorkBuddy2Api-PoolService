package agentcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Codex 的 config.toml 用「标记块 + 顶层键」两种方式写入：
//   - [model_providers.poolservice] 放进一对注释标记之间，更新 / 移除时整块替换；
//   - model / model_provider 是顶层键，只在用户勾选「设为默认」时才改，改之前记下原值。
//
// 不用 TOML 库回写：库会丢掉注释与排版。库只用来读取现有值，以及在写完后做两项校验：
// 结果仍是合法 TOML，且除受管键之外的所有内容与原文件逐项相等。
const (
	codexBegin = "# >>> PoolService managed >>>"
	codexEnd   = "# <<< PoolService managed <<<"
)

type codex struct{}

func (codex) ID() string   { return "codex" }
func (codex) Name() string { return "Codex CLI" }

func (codex) dir(env Env, dirOpt string) string {
	if dirOpt != "" {
		return env.path(dirOpt)
	}
	if v := env.env("CODEX_HOME"); v != "" {
		return env.path(v)
	}
	return filepath.Join(env.Home, ".codex")
}

func (c codex) file(env Env, dirOpt string) string {
	return filepath.Join(c.dir(env, dirOpt), "config.toml")
}

var tomlHeader = regexp.MustCompile(`^\s*\[\[?[^\[\]=]+\]\]?\s*(#.*)?$`)

func tomlLines(s string) ([]string, string) {
	eol := "\n"
	if strings.Contains(s, "\r\n") {
		eol = "\r\n"
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), eol
}

func tomlStr(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(buf.String(), "\n")
}

// cutBlock 去掉受管标记块（含前面的一个空行）。
func cutBlock(lines []string) ([]string, bool, error) {
	b, e := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case codexBegin:
			b = i
		case codexEnd:
			e = i
		}
	}
	if b < 0 && e < 0 {
		return lines, false, nil
	}
	if b < 0 || e < b {
		return nil, false, errors.New("受管标记块不完整（只找到开始或结束标记），请手动检查 config.toml")
	}
	start := b
	if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
		start--
	}
	out := append(append([]string(nil), lines[:start]...), lines[e+1:]...)
	return out, true, nil
}

func topEnd(lines []string) int {
	for i, l := range lines {
		if tomlHeader.MatchString(l) {
			return i
		}
	}
	return len(lines)
}

func topKeyRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
}

func setTopKey(lines []string, key, literal string) []string {
	end := topEnd(lines)
	re := topKeyRe(key)
	for i := 0; i < end; i++ {
		if re.MatchString(lines[i]) {
			lines[i] = key + " = " + literal
			return lines
		}
	}
	return append([]string{key + " = " + literal}, lines...)
}

func delTopKey(lines []string, key string) []string {
	end := topEnd(lines)
	re := topKeyRe(key)
	for i := 0; i < end; i++ {
		if re.MatchString(lines[i]) {
			return append(append([]string(nil), lines[:i]...), lines[i+1:]...)
		}
	}
	return lines
}

func joinLines(lines []string, eol string) string { return strings.Join(lines, eol) }

func parseTOML(s string) (map[string]any, error) {
	m := map[string]any{}
	if err := toml.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func codexProviderOf(m map[string]any) (map[string]any, bool) {
	mp, ok := m["model_providers"].(map[string]any)
	if !ok {
		return nil, false
	}
	p, ok := mp[ProviderID].(map[string]any)
	return p, ok
}

// stripManaged 去掉受管键，供前后对比。
func stripManaged(m map[string]any, topKeys ...string) {
	for _, k := range topKeys {
		delete(m, k)
	}
	if mp, ok := m["model_providers"].(map[string]any); ok {
		delete(mp, ProviderID)
		if len(mp) == 0 {
			delete(m, "model_providers")
		}
	}
}

func verifyUntouched(before, after string, topKeys ...string) error {
	b, err := parseTOML(before)
	if err != nil {
		return nil // 原文件本就不合法时由调用方先行报错
	}
	a, err := parseTOML(after)
	if err != nil {
		return fmt.Errorf("写入后的内容不是合法的 TOML：%w", err)
	}
	stripManaged(b, topKeys...)
	stripManaged(a, topKeys...)
	if !reflect.DeepEqual(b, a) {
		return errors.New("内部校验失败：写入会改动受管范围之外的配置，已放弃")
	}
	return nil
}

func (c codex) Detect(env Env, opt Options, _ Prev) Info {
	file := c.file(env, opt.Dir)
	info := Info{
		ID: c.ID(), Name: c.Name(), Protocol: "responses",
		Files:           fileInfos(file),
		SupportsDefault: true,
		Restart:         "新开的 codex 会话生效",
	}
	if d := c.dir(env, opt.Dir); isDir(d) {
		info.Evidence = append(info.Evidence, d)
	}
	info.Evidence = append(info.Evidence, env.lookPath("codex")...)
	info.Installed = len(info.Evidence) > 0
	b, ok, err := readOpt(file)
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	if !ok {
		return info
	}
	m, err := parseTOML(strings.TrimPrefix(string(b), utf8BOM))
	if err != nil {
		info.Blocked = fmt.Sprintf("%s 不是合法的 TOML：%v", file, err)
		return info
	}
	if _, has := codexProviderOf(m); has {
		info.Imported = true
		if mp, _ := m["model_provider"].(string); mp == ProviderID {
			if model, _ := m["model"].(string); model != "" {
				info.ImportedModels = []string{model}
			}
		}
	}
	return info
}

func (c codex) block(in Input) []string {
	return []string{
		codexBegin,
		"[model_providers." + ProviderID + "]",
		"name = " + tomlStr(ProviderName),
		"base_url = " + tomlStr(in.OpenAIBase()),
		`wire_api = "responses"`,
		"requires_openai_auth = false",
		"experimental_bearer_token = " + tomlStr(in.APIKey),
		codexEnd,
	}
}

func (c codex) Build(env Env, in Input, prev Prev) (*Result, error) {
	file := c.file(env, in.Dir)
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	bom := strings.HasPrefix(string(before), utf8BOM)
	text := strings.TrimPrefix(string(before), utf8BOM)
	lines, eol := tomlLines(text)
	lines, _, err = cutBlock(lines)
	if err != nil {
		return nil, err
	}
	clean, err := parseTOML(joinLines(lines, eol))
	if err != nil {
		return nil, fmt.Errorf("%s 不是合法的 TOML，未做任何修改：%w", file, err)
	}
	if _, dup := codexProviderOf(clean); dup {
		return nil, fmt.Errorf("%s 里已经有名为 %s 的自定义供应商，且不是本工具写入的；为避免覆盖，请先改名或删除它", file, ProviderID)
	}

	p := copyPrev(prev)
	res := &Result{}
	managedTop := []string{}
	if in.Default != "" {
		for _, kv := range [][2]string{{"model", in.Default}, {"model_provider", ProviderID}} {
			key, val := kv[0], kv[1]
			if _, ok := p[key]; !ok {
				old, has := clean[key].(string)
				pv := PrevVal{Present: has}
				if has {
					pv.Raw, _ = json.Marshal(old)
				}
				p[key] = pv
			}
			lines = setTopKey(lines, key, tomlStr(val))
			managedTop = append(managedTop, key)
		}
		res.Imported = []string{in.Default}
	} else {
		res.Warnings = append(res.Warnings, "未设为默认模型：供应商已写入，使用时请加 -c model_provider=poolservice -m <模型>，或在 config.toml 里自行设置 model_provider")
	}

	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	blk := c.block(in)
	lines = append(lines, blk...)
	lines = append(lines, "")
	out := joinLines(lines, eol)
	if bom {
		out = utf8BOM + out
	}
	if err := verifyUntouched(text, strings.TrimPrefix(out, utf8BOM), "model", "model_provider"); err != nil {
		return nil, err
	}
	after, _ := parseTOML(strings.TrimPrefix(out, utf8BOM))
	if got, ok := codexProviderOf(after); !ok || got["base_url"] != in.OpenAIBase() {
		return nil, errors.New("内部校验失败：写入后未读到预期的供应商配置，已放弃")
	}
	res.Prev = p
	snippet := strings.Join(blk[1:len(blk)-1], "\n")
	if len(managedTop) > 0 {
		snippet = "model = " + tomlStr(in.Default) + "\nmodel_provider = " + tomlStr(ProviderID) + "\n\n" + snippet
	}
	res.Changes = []Change{{
		Path: file, Before: before, Existed: existed, After: []byte(out),
		Snippet: snippet, Lang: "toml", Secrets: []string{in.APIKey},
	}}
	return res, nil
}

func (c codex) Remove(env Env, opt Options, prev Prev) (*Result, error) {
	file := c.file(env, opt.Dir)
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	if !existed {
		return nil, fmt.Errorf("没有找到 %s，无需移除", file)
	}
	bom := strings.HasPrefix(string(before), utf8BOM)
	text := strings.TrimPrefix(string(before), utf8BOM)
	lines, eol := tomlLines(text)
	lines, found, err := cutBlock(lines)
	if err != nil {
		return nil, err
	}
	cur, err := parseTOML(text)
	if err != nil {
		return nil, fmt.Errorf("%s 不是合法的 TOML，未做任何修改：%w", file, err)
	}
	usingUs, _ := cur["model_provider"].(string)
	changed := found
	for _, key := range []string{"model", "model_provider"} {
		pv, ok := prev[key]
		switch {
		case ok && pv.Present:
			var old string
			if json.Unmarshal(pv.Raw, &old) == nil {
				lines = setTopKey(lines, key, tomlStr(old))
				changed = true
			}
		case ok && !pv.Present:
			lines = delTopKey(lines, key)
			changed = true
		case !ok && usingUs == ProviderID:
			lines = delTopKey(lines, key)
			changed = true
		}
	}
	if !changed {
		return nil, errors.New("没有发现本工具写入的 Codex 配置，无需移除")
	}
	out := joinLines(lines, eol)
	if bom {
		out = utf8BOM + out
	}
	if _, err := parseTOML(strings.TrimPrefix(out, utf8BOM)); err != nil {
		return nil, fmt.Errorf("内部校验失败：移除后的内容不是合法的 TOML，已放弃：%w", err)
	}
	return &Result{
		ClearPrev: true,
		Changes:   []Change{{Path: file, Before: before, Existed: true, After: []byte(out)}},
	}, nil
}
