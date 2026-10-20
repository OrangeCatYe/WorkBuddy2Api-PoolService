package agentcfg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---- OMP ----

// ompAgent 写 models.yml 的 providers.poolservice。
//
// 配置目录的优先级：手动指定 > profile（OMP_PROFILE / PI_PROFILE / 面板里选择）> PI_CODING_AGENT_DIR > ~/.omp/agent。
// 受管标记用键上的注释，OMP 自己不认识额外字段，注释最安全。
type ompAgent struct{}

func (ompAgent) ID() string   { return "omp" }
func (ompAgent) Name() string { return "OMP" }

const ompMark = "PoolService managed"

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (ompAgent) root(env Env) string {
	c := env.env("PI_CONFIG_DIR")
	if c == "" {
		c = ".omp"
	}
	return env.path(c)
}

func (o ompAgent) agentDir(env Env, opt Options) (string, error) {
	if opt.Dir != "" {
		return env.path(opt.Dir), nil
	}
	prof := strings.TrimSpace(opt.Profile)
	if prof == "" {
		if prof = env.env("OMP_PROFILE"); prof == "" {
			prof = env.env("PI_PROFILE")
		}
	}
	if prof != "" {
		if !profileNameRe.MatchString(prof) || prof == "." || prof == ".." {
			return "", fmt.Errorf("OMP 档案名 %q 无效", prof)
		}
		return filepath.Join(o.root(env), "profiles", prof, "agent"), nil
	}
	if v := env.env("PI_CODING_AGENT_DIR"); v != "" {
		return env.path(v), nil
	}
	return filepath.Join(o.root(env), "agent"), nil
}

// modelsFile 返回要写的文件；只有旧版 models.json 而没有 yml 时 legacy=true（需要先让 OMP 自己迁移）。
func ompModelsFile(dir string) (string, bool) {
	for _, n := range []string{"models.yml", "models.yaml"} {
		if p := filepath.Join(dir, n); exists(p) {
			return p, false
		}
	}
	if exists(filepath.Join(dir, "models.json")) {
		return filepath.Join(dir, "models.yml"), true
	}
	return filepath.Join(dir, "models.yml"), false
}

func (o ompAgent) profiles(env Env) []Choice {
	out := []Choice{{ID: "", Label: "默认", Hint: "不使用档案"}}
	ents, _ := os.ReadDir(filepath.Join(o.root(env), "profiles"))
	var names []string
	for _, e := range ents {
		if e.IsDir() && profileNameRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, Choice{ID: n, Label: n})
	}
	return out
}

var ompEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

func ompThinking(m Model) Obj {
	set := effortSet(m)
	var lv []string
	for _, e := range ompEfforts {
		if set[e] {
			lv = append(lv, e)
		}
	}
	if len(lv) == 0 {
		return nil
	}
	o := Obj{{"mode", "effort"}, {"efforts", lv}}
	if set["high"] {
		o = append(o, KV{"defaultLevel", "high"})
	}
	return o
}

func ompEntry(in Input) Obj {
	var models []Obj
	for _, m := range in.Models {
		mo := Obj{{"id", m.ID}, {"name", displayName(m)}, {"reasoning", m.Reasoning}, {"input", inputModalities(m)}}
		if m.Context > 0 {
			mo = append(mo, KV{"contextWindow", m.Context})
		}
		if m.MaxOutput > 0 {
			mo = append(mo, KV{"maxTokens", m.MaxOutput})
		}
		if th := ompThinking(m); th != nil {
			mo = append(mo, KV{"thinking", th})
		}
		models = append(models, mo)
	}
	return Obj{
		{"baseUrl", in.OpenAIBase()},
		{"apiKey", in.APIKey},
		{"api", "openai-completions"},
		{"compat", Obj{{"supportsDeveloperRole", false}, {"supportsStore", false}, {"supportsStrictMode", false}, {"maxTokensField", "max_tokens"}, {"thinkingFormat", "openai"}}},
		{"models", models},
	}
}

func ompOwned(providers *yaml.Node) (owned, present bool) {
	v, i := ymapGet(providers, ProviderID)
	if v == nil {
		return false, false
	}
	k := providers.Content[i]
	if strings.Contains(k.HeadComment, ompMark) || strings.Contains(k.LineComment, ompMark) {
		return true, true
	}
	key, _ := ymapGet(v, "apiKey")
	return strings.HasPrefix(yscalar(key), "sk-pool-"), true
}

func (o ompAgent) Detect(env Env, opt Options, _ Prev) Info {
	info := Info{
		ID: o.ID(), Name: o.Name(), Protocol: "chat",
		Profiles: o.profiles(env), Profile: opt.Profile,
		Restart: "重启 omp",
		Notes:   []string{"OMP 在会话内选择模型，这里只登记模型"},
	}
	dir, err := o.agentDir(env, opt)
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	file, legacy := ompModelsFile(dir)
	info.Files = fileInfos(file)
	if isDir(dir) {
		info.Evidence = append(info.Evidence, dir)
	}
	info.Evidence = append(info.Evidence, env.lookPath("omp")...)
	info.Installed = len(info.Evidence) > 0
	if legacy {
		info.Blocked = "发现尚未迁移的旧版 models.json，请先启动一次 OMP 完成迁移，再回来导入"
		return info
	}
	b, ok, err := readOpt(file)
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	if !ok {
		return info
	}
	f, err := loadYAML(b, yaml.MappingNode)
	if err != nil {
		info.Blocked = fmt.Sprintf("%s 无法解析：%v", file, err)
		return info
	}
	providers, _ := ymapGet(f.root, "providers")
	if owned, present := ompOwned(providers); present && owned {
		info.Imported = true
		v, _ := ymapGet(providers, ProviderID)
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

func (o ompAgent) Build(env Env, in Input, _ Prev) (*Result, error) {
	if len(in.Models) == 0 {
		return nil, errors.New("请至少选择一个模型")
	}
	dir, err := o.agentDir(env, Options{Profile: in.Profile, Dir: in.Dir})
	if err != nil {
		return nil, err
	}
	file, legacy := ompModelsFile(dir)
	if legacy {
		return nil, errors.New("发现尚未迁移的旧版 models.json，请先启动一次 OMP 完成迁移，再回来导入")
	}
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	f, err := loadYAML(before, yaml.MappingNode)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", file, err)
	}
	providers, err := yensureMap(f.root, "providers")
	if err != nil {
		return nil, err
	}
	if owned, present := ompOwned(providers); present && !owned {
		return nil, fmt.Errorf("%s 里已经有名为 %s 的供应商，且不是本工具写入的；为避免覆盖，请先改名或删除它", file, ProviderID)
	}
	entry := ompEntry(in)
	node, err := yamlNode(entry)
	if err != nil {
		return nil, err
	}
	ymapSet(providers, ProviderID, node)
	if _, i := ymapGet(providers, ProviderID); i >= 0 && !strings.Contains(providers.Content[i].HeadComment, ompMark) {
		providers.Content[i].HeadComment = "# " + ompMark + "：由 PoolService 面板写入，请在面板里更新或移除"
	}
	after, err := f.bytes()
	if err != nil {
		return nil, err
	}
	if _, err := loadYAML(after, yaml.MappingNode); err != nil {
		return nil, fmt.Errorf("内部校验失败：写入后的 YAML 无法解析，已放弃：%w", err)
	}
	res := &Result{}
	for _, m := range in.Models {
		res.Imported = append(res.Imported, m.ID)
	}
	res.Changes = []Change{{
		Path: file, Before: before, Existed: existed, After: after,
		Snippet: yamlText(Obj{{"providers", Obj{{ProviderID, entry}}}}), Lang: "yaml", Secrets: []string{in.APIKey},
	}}
	return res, nil
}

func (o ompAgent) Remove(env Env, opt Options, _ Prev) (*Result, error) {
	dir, err := o.agentDir(env, opt)
	if err != nil {
		return nil, err
	}
	file, legacy := ompModelsFile(dir)
	if legacy {
		return nil, errors.New("发现尚未迁移的旧版 models.json，请先启动一次 OMP 完成迁移")
	}
	before, existed, err := readOpt(file)
	if err != nil {
		return nil, err
	}
	if !existed {
		return nil, fmt.Errorf("没有找到 %s，无需移除", file)
	}
	f, err := loadYAML(before, yaml.MappingNode)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", file, err)
	}
	providers, _ := ymapGet(f.root, "providers")
	if owned, present := ompOwned(providers); !present || !owned {
		return nil, errors.New("没有发现本工具写入的 OMP 配置，无需移除")
	}
	ymapDel(providers, ProviderID)
	after, err := f.bytes()
	if err != nil {
		return nil, err
	}
	return &Result{Changes: []Change{{Path: file, Before: before, Existed: true, After: after}}}, nil
}
