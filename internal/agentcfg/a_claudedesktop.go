package agentcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---- Claude Desktop ----

// claudeDesktop 通过 3P（第三方推理网关）模式接入：
//
//	<Claude-3p>/configLibrary/<id>.json   网关地址、密钥与模型清单（我们独占的档案）
//	<Claude-3p>/configLibrary/_meta.json  档案列表与当前启用的档案
//	<Claude>/claude_desktop_config.json、<Claude-3p>/claude_desktop_config.json  deploymentMode 切到 3p
//
// 这是「接管」：切到 3p 后 Claude Desktop 不再走官方账号。被改动的 deploymentMode 与 appliedId 会记下原值，移除时还原。
// Claude Desktop 的模型菜单只接受 claude-<家族>-… 命名的模型，其余模型无法在它里面使用，会被跳过并说明。
type claudeDesktop struct{}

func (claudeDesktop) ID() string   { return "claude-desktop" }
func (claudeDesktop) Name() string { return "Claude Desktop" }

// cdProfileID 与 ttswitch 使用的档案 ID 不同，两个工具不会互相覆盖档案文件。
const cdProfileID = "00000000-0000-4000-8000-0000000900a1"

type cdPaths struct {
	normalDir, threepDir string
	normalCfg, threepCfg string
	profile, meta        string
}

func cdPick(local string, threep bool) string {
	want := "Claude"
	if threep {
		want = "Claude-3p"
	}
	exact := filepath.Join(local, want)
	if isDir(exact) {
		return exact
	}
	ents, _ := os.ReadDir(local)
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() && strings.HasPrefix(n, "Claude") && strings.Contains(n, "-3p") == threep {
			return filepath.Join(local, n)
		}
	}
	return exact
}

func (claudeDesktop) paths(env Env, dirOpt string) (cdPaths, error) {
	var p cdPaths
	switch {
	case dirOpt != "":
		p.threepDir = env.path(dirOpt)
		p.normalDir = filepath.Join(filepath.Dir(p.threepDir), "Claude")
	case env.GOOS == "windows":
		local := env.LocalAppData
		if local == "" {
			local = filepath.Join(env.Home, "AppData", "Local")
		}
		p.normalDir, p.threepDir = cdPick(local, false), cdPick(local, true)
	case env.GOOS == "darwin":
		base := filepath.Join(env.Home, "Library", "Application Support")
		p.normalDir, p.threepDir = filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")
	default:
		return p, errors.New("Claude Desktop 目前只支持 Windows 和 macOS")
	}
	p.normalCfg = filepath.Join(p.normalDir, "claude_desktop_config.json")
	p.threepCfg = filepath.Join(p.threepDir, "claude_desktop_config.json")
	lib := filepath.Join(p.threepDir, "configLibrary")
	p.profile = filepath.Join(lib, cdProfileID+".json")
	p.meta = filepath.Join(lib, "_meta.json")
	return p, nil
}

var cdSafeChars = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// claudeFamily 返回 claude-<家族>-… 命名对应的家族；不符合返回空串。
func claudeFamily(id string) string {
	n := strings.ToLower(strings.TrimSpace(id))
	if strings.Contains(n, "[1m]") || !cdSafeChars.MatchString(n) {
		return ""
	}
	tail := strings.TrimPrefix(n, "anthropic/claude-")
	if tail == n {
		if tail = strings.TrimPrefix(n, "claude-"); tail == n {
			return ""
		}
	}
	for _, fam := range []string{"sonnet", "opus", "haiku", "fable", "mythos"} {
		if strings.HasPrefix(tail, fam+"-") && len(tail) > len(fam)+1 {
			return fam
		}
	}
	return ""
}

func (c claudeDesktop) Detect(env Env, opt Options, _ Prev) Info {
	info := Info{
		ID: c.ID(), Name: c.Name(), Protocol: "anthropic", Takeover: true,
		Restart: "完全退出 Claude Desktop（包括托盘图标）后重新打开",
		Notes: []string{
			"Claude Desktop 的模型菜单只接受 claude-<家族>-… 命名的模型，其他模型会被自动跳过",
			"写入后 Claude Desktop 会切换到第三方网关模式，不再走官方账号；移除时还原",
		},
	}
	p, err := c.paths(env, opt.Dir)
	if err != nil {
		info.Blocked = err.Error()
		return info
	}
	info.Files = fileInfos(p.threepCfg, p.profile, p.meta)
	if exists(p.normalCfg) {
		info.Files = append(info.Files, FileInfo{Path: p.normalCfg, Exists: true})
	}
	for _, d := range []string{p.normalDir, p.threepDir} {
		if isDir(d) {
			info.Evidence = append(info.Evidence, d)
		}
	}
	info.Installed = len(info.Evidence) > 0
	if !info.Installed {
		info.Blocked = "没有找到 Claude Desktop 的数据目录，请先安装并启动一次，或手动指定 Claude-3p 目录"
		return info
	}
	if b, ok, err := readOpt(p.meta); err == nil && ok {
		if ed, err := newJSONEditor(b); err == nil {
			var applied string
			_, _ = ed.Get(&applied, "appliedId")
			if applied != "" && applied != cdProfileID {
				name := applied
				for i := 0; i < ed.Len("entries"); i++ {
					var e struct{ ID, Name string }
					if ed.Elem(&e, i, "entries") == nil && e.ID == applied && e.Name != "" {
						name = e.Name
					}
				}
				info.Notes = append(info.Notes, fmt.Sprintf("当前启用的是「%s」配置，写入后会切换到 PoolService，移除时切回", name))
			}
		}
	}
	if b, ok, err := readOpt(p.profile); err == nil && ok {
		if ed, err := newJSONEditor(b); err == nil {
			info.Imported = true
			for i := 0; i < ed.Len("inferenceModels"); i++ {
				var m struct {
					Name string `json:"name"`
				}
				if ed.Elem(&m, i, "inferenceModels") == nil {
					info.ImportedModels = append(info.ImportedModels, m.Name)
				}
			}
		}
	}
	return info
}

func (c claudeDesktop) Build(env Env, in Input, prev Prev) (*Result, error) {
	p, err := c.paths(env, in.Dir)
	if err != nil {
		return nil, err
	}
	if !isDir(p.threepDir) && !isDir(p.normalDir) {
		return nil, errors.New("没有找到 Claude Desktop 的数据目录，请先安装并启动一次，或手动指定 Claude-3p 目录")
	}
	res := &Result{}
	type route struct {
		m   Model
		fam string
	}
	var routes []route
	for _, m := range in.Models {
		fam := claudeFamily(m.ID)
		if fam == "" {
			res.Skipped = append(res.Skipped, Skip{m.ID, "不是 claude-<家族>-… 命名，Claude Desktop 的模型菜单不接受"})
			continue
		}
		routes = append(routes, route{m, fam})
	}
	if len(routes) == 0 {
		return nil, errors.New("所选模型里没有 claude-<家族>-… 命名的模型（例如 claude-sonnet-5），Claude Desktop 无法使用")
	}
	seenFam := map[string]bool{}
	var infer []Obj
	for _, r := range routes {
		o := Obj{
			{"name", r.m.ID},
			{"labelOverride", displayName(r.m)},
			{"anthropicFamilyTier", r.fam},
			{"isFamilyDefault", !seenFam[r.fam]},
			{"supports1m", r.m.Context >= 1_000_000},
			{"prefer1m", false},
		}
		seenFam[r.fam] = true
		infer = append(infer, o)
		res.Imported = append(res.Imported, r.m.ID)
	}
	profile := Obj{
		{"disableDeploymentModeChooser", true},
		{"inferenceGatewayApiKey", in.APIKey},
		{"inferenceGatewayAuthScheme", "bearer"},
		{"inferenceGatewayBaseUrl", in.AnthropicBase()},
		{"inferenceProvider", "gateway"},
		{"modelDiscoveryEnabled", false},
		{"modelPrefer1mContext", false},
		{"inferenceModels", infer},
	}
	pj, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return nil, err
	}
	pj = append(pj, '\n')

	prevMap := copyPrev(prev)
	profBefore, profExisted, err := readOpt(p.profile)
	if err != nil {
		return nil, err
	}
	res.Changes = append(res.Changes, Change{
		Path: p.profile, Before: profBefore, Existed: profExisted, After: pj,
		Snippet: string(pj), Lang: "json", Secrets: []string{in.APIKey},
	})

	// meta：登记档案并设为当前启用。
	metaBefore, metaExisted, err := readOpt(p.meta)
	if err != nil {
		return nil, err
	}
	med, err := newJSONEditor(metaBefore)
	if err != nil {
		return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", p.meta, err)
	}
	if med.Kind() != '{' {
		return nil, fmt.Errorf("%s 的根节点不是对象", p.meta)
	}
	if k := med.Kind("entries"); k == 0 {
		if err := med.Set([]any{}, "entries"); err != nil {
			return nil, err
		}
	} else if k != '[' {
		return nil, fmt.Errorf("%s 中的 entries 不是数组", p.meta)
	}
	var applied string
	_, _ = med.Get(&applied, "appliedId")
	if applied != "" && applied != cdProfileID {
		res.Warnings = append(res.Warnings, "Claude Desktop 当前启用的是另一份配置（"+applied+"），写入后会切换到 PoolService；如果有其他工具（如 ttswitch）在管理它，之后它们再次写入时会切回去")
	}
	entry := Obj{{"id", cdProfileID}, {"name", ProviderName}}
	found := -1
	for i := 0; i < med.Len("entries"); i++ {
		var e struct{ ID string }
		if med.Elem(&e, i, "entries") == nil && e.ID == cdProfileID {
			found = i
		}
	}
	if found >= 0 {
		err = med.ArraySet(entry, found, "entries")
	} else {
		err = med.ArrayAppend(entry, "entries")
	}
	if err != nil {
		return nil, err
	}
	if err := setWithPrev(med, prevMap, "cd.appliedId", cdProfileID, "appliedId"); err != nil {
		return nil, err
	}
	res.Changes = append(res.Changes, Change{Path: p.meta, Before: metaBefore, Existed: metaExisted, After: med.Bytes()})

	// 两份 claude_desktop_config.json：切到 3p。
	for _, f := range []struct{ path, key string }{{p.threepCfg, "cd.threep.mode"}, {p.normalCfg, "cd.normal.mode"}} {
		before, existed, err := readOpt(f.path)
		if err != nil {
			return nil, err
		}
		if !existed && !isDir(filepath.Dir(f.path)) {
			continue
		}
		ed, err := newJSONEditor(before)
		if err != nil {
			return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", f.path, err)
		}
		if ed.Kind() != '{' {
			return nil, fmt.Errorf("%s 的根节点不是对象", f.path)
		}
		if err := setWithPrev(ed, prevMap, f.key, "3p", "deploymentMode"); err != nil {
			return nil, err
		}
		res.Changes = append(res.Changes, Change{Path: f.path, Before: before, Existed: existed, After: ed.Bytes()})
	}
	res.Prev = prevMap
	return res, nil
}

func (c claudeDesktop) Remove(env Env, opt Options, prev Prev) (*Result, error) {
	p, err := c.paths(env, opt.Dir)
	if err != nil {
		return nil, err
	}
	res := &Result{ClearPrev: true}
	profBefore, profExisted, err := readOpt(p.profile)
	if err != nil {
		return nil, err
	}
	if profExisted {
		res.Changes = append(res.Changes, Change{Path: p.profile, Before: profBefore, Existed: true, Delete: true})
	}

	metaBefore, metaExisted, err := readOpt(p.meta)
	if err != nil {
		return nil, err
	}
	if metaExisted {
		med, err := newJSONEditor(metaBefore)
		if err != nil {
			return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", p.meta, err)
		}
		for i := med.Len("entries") - 1; i >= 0; i-- {
			var e struct{ ID string }
			if med.Elem(&e, i, "entries") == nil && e.ID == cdProfileID {
				if err := med.ArrayDelete(i, "entries"); err != nil {
					return nil, err
				}
			}
		}
		var applied string
		_, _ = med.Get(&applied, "appliedId")
		if applied == cdProfileID {
			if ok, err := restorePrev(med, prev, "cd.appliedId", "appliedId"); err != nil {
				return nil, err
			} else if !ok {
				if err := med.Delete("appliedId"); err != nil {
					return nil, err
				}
			}
		}
		res.Changes = append(res.Changes, Change{Path: p.meta, Before: metaBefore, Existed: true, After: med.Bytes()})
	}
	if !profExisted && !metaExisted {
		return nil, errors.New("没有发现本工具写入的 Claude Desktop 配置，无需移除")
	}
	for _, f := range []struct{ path, key string }{{p.threepCfg, "cd.threep.mode"}, {p.normalCfg, "cd.normal.mode"}} {
		pv, ok := prev[f.key]
		if !ok {
			continue
		}
		before, existed, err := readOpt(f.path)
		if err != nil {
			return nil, err
		}
		if !existed {
			continue
		}
		ed, err := newJSONEditor(before)
		if err != nil {
			return nil, fmt.Errorf("%s 无法解析，未做任何修改：%w", f.path, err)
		}
		if _, err := restorePrev(ed, Prev{f.key: pv}, f.key, "deploymentMode"); err != nil {
			return nil, err
		}
		res.Changes = append(res.Changes, Change{Path: f.path, Before: before, Existed: true, After: ed.Bytes()})
	}
	if len(prev) == 0 {
		res.Warnings = append(res.Warnings, "没有找到还原记录，已移除 PoolService 档案，但 deploymentMode 保持不变；如需回到官方模式，请在 Claude Desktop 的设置里切换")
	}
	return res, nil
}
