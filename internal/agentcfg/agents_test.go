package agentcfg

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func fakeClock(sec int) func() time.Time {
	return func() time.Time { return time.Date(2026, 1, 1, 0, 0, sec, 0, time.UTC) }
}

const testKey = "sk-pool-0123456789abcdef0123456789abcdef01234567"

var testModels = []Model{
	{ID: "glm-5.2", Name: "GLM 5.2", Context: 200000, MaxOutput: 32000, Tools: true, Reasoning: true, Efforts: []string{"low", "medium", "high"}},
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5", Context: 1000000, MaxOutput: 64000, Vision: true, Tools: true},
	{ID: "hy3", Name: "hy3", Tools: true},
}

func testInput(models ...Model) Input {
	if len(models) == 0 {
		models = testModels
	}
	return Input{BaseURL: "http://127.0.0.1:7863", APIKey: testKey, Models: models}
}

func testEnv(t *testing.T, kv ...string) Env {
	t.Helper()
	home := t.TempDir()
	vars := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = strings.ReplaceAll(kv[i+1], "{home}", home)
	}
	return Env{
		GOOS:         runtime.GOOS,
		Home:         home,
		AppData:      filepath.Join(home, "AppData", "Roaming"),
		LocalAppData: filepath.Join(home, "AppData", "Local"),
		Getenv:       func(k string) string { return vars[k] },
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s：%v", path, err)
	}
	return string(b)
}

func jsonMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(standardize(strings.TrimPrefix(s, utf8BOM))), &m); err != nil {
		t.Fatalf("不是合法 JSON：%v\n%s", err, s)
	}
	return m
}

func yamlAny(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("不是合法 YAML：%v\n%s", err, s)
	}
	return v
}

type harness struct {
	t   *testing.T
	env Env
	mgr *Manager
	a   Adapter
}

func newHarness(t *testing.T, a Adapter, env Env) *harness {
	return &harness{t: t, env: env, mgr: NewManager(t.TempDir()), a: a}
}

func (h *harness) build(in Input) *Result {
	h.t.Helper()
	res, err := h.a.Build(h.env, in, h.mgr.LoadPrev(h.a.ID()))
	if err != nil {
		h.t.Fatalf("Build: %v", err)
	}
	if _, err := h.mgr.Apply(h.a.ID(), res); err != nil {
		h.t.Fatalf("Apply: %v", err)
	}
	return res
}

func (h *harness) remove(opt Options) *Result {
	h.t.Helper()
	res, err := h.a.Remove(h.env, opt, h.mgr.LoadPrev(h.a.ID()))
	if err != nil {
		h.t.Fatalf("Remove: %v", err)
	}
	if _, err := h.mgr.Apply(h.a.ID(), res); err != nil {
		h.t.Fatalf("Apply(remove): %v", err)
	}
	return res
}

func (h *harness) detect(opt Options) Info {
	return h.a.Detect(h.env, opt, h.mgr.LoadPrev(h.a.ID()))
}

// ---- WorkBuddy / CodeBuddy ----

func TestModelsJSONMergeConflictUpdateRemove(t *testing.T) {
	env := testEnv(t)
	a := modelsJSON{id: "workbuddy", name: "WorkBuddy", dirName: ".workbuddy"}
	file := filepath.Join(env.Home, ".workbuddy", "models.json")
	orig := "{\n  \"models\": [\n    {\n      \"id\": \"deepseek-chat\",\n      \"name\": \"DeepSeek\"\n    },\n    {\n      \"id\": \"hy3\",\n      \"name\": \"mine\"\n    }\n  ],\n  \"availableModels\": [\"deepseek-chat\", \"hy3\"]\n}\n"
	writeFile(t, file, orig)
	h := newHarness(t, a, env)

	res := h.build(testInput())
	if !reflect.DeepEqual(res.Imported, []string{"glm-5.2", "claude-sonnet-5"}) {
		t.Fatalf("Imported = %v", res.Imported)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != "hy3" {
		t.Fatalf("同名的用户模型应被跳过：%+v", res.Skipped)
	}
	got := readFile(t, file)
	if !strings.Contains(got, "\"name\": \"mine\"") || !strings.Contains(got, "\"name\": \"DeepSeek\"") {
		t.Fatalf("用户自己的模型被改动：\n%s", got)
	}
	m := jsonMap(t, got)
	if n := len(m["models"].([]any)); n != 4 {
		t.Fatalf("models 条数 = %d", n)
	}
	avail := m["availableModels"].([]any)
	if len(avail) != 4 || avail[2] != "glm-5.2" {
		t.Fatalf("availableModels = %v", avail)
	}
	ent := m["models"].([]any)[2].(map[string]any)
	if ent["vendor"] != vendorMark || ent["url"] != "http://127.0.0.1:7863/v1/chat/completions" || ent["apiKey"] != testKey {
		t.Fatalf("条目内容不符：%v", ent)
	}
	if ent["maxInputTokens"].(float64) != 200000 || ent["supportsReasoning"] != true {
		t.Fatalf("能力字段不符：%v", ent)
	}

	if info := h.detect(Options{}); !info.Imported || len(info.ImportedModels) != 2 {
		t.Fatalf("Detect = %+v", info)
	}

	// 更新：只保留 glm-5.2，另一条受管条目应被删掉。
	h.build(testInput(testModels[0]))
	m = jsonMap(t, readFile(t, file))
	if n := len(m["models"].([]any)); n != 3 {
		t.Fatalf("更新后 models 条数 = %d", n)
	}
	if strings.Contains(readFile(t, file), "claude-sonnet-5") {
		t.Fatal("不再选择的受管条目应被删除，availableModels 里也不应残留")
	}

	h.remove(Options{})
	if got := readFile(t, file); got != orig {
		t.Fatalf("移除后应还原为原文件\n想要：%q\n实际：%q", orig, got)
	}
}

func TestModelsJSONFreshAndRootArray(t *testing.T) {
	env := testEnv(t)
	a := modelsJSON{id: "codebuddy", name: "CodeBuddy", dirName: ".codebuddy"}
	h := newHarness(t, a, env)
	h.build(testInput())
	file := filepath.Join(env.Home, ".codebuddy", "models.json")
	m := jsonMap(t, readFile(t, file))
	if len(m["models"].([]any)) != 3 {
		t.Fatalf("新建文件内容不符：%v", m)
	}
	h.remove(Options{})

	env2 := testEnv(t)
	file2 := filepath.Join(env2.Home, ".codebuddy", "models.json")
	writeFile(t, file2, `[{"id":"x","name":"X"}]`)
	h2 := newHarness(t, a, env2)
	h2.build(testInput(testModels[0]))
	var arr []map[string]any
	if err := json.Unmarshal([]byte(readFile(t, file2)), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("根为数组的文件应在数组里追加：%v %v", err, arr)
	}
}

func TestModelsJSONBadFileIsNotTouched(t *testing.T) {
	env := testEnv(t)
	a := modelsJSON{id: "workbuddy", name: "WorkBuddy", dirName: ".workbuddy"}
	file := filepath.Join(env.Home, ".workbuddy", "models.json")
	writeFile(t, file, "{ 'single': 1 }")
	if _, err := a.Build(env, testInput(), nil); err == nil {
		t.Fatal("无法解析的文件应当拒绝写入")
	}
	if info := a.Detect(env, Options{}, nil); info.Blocked == "" {
		t.Fatal("Detect 应当给出 Blocked 原因")
	}
	if readFile(t, file) != "{ 'single': 1 }" {
		t.Fatal("文件被改动")
	}
}

// ---- Claude Code ----

func TestClaudeCodeTakeoverAndRestore(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".claude", "settings.json")
	orig := "{\n  // 我的设置\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"https://old.example.com\",\n    \"ANTHROPIC_AUTH_TOKEN\": \"old-token\",\n    \"KEEP\": \"1\"\n  },\n  \"permissions\": {\n    \"allow\": [\"Bash(ls)\"]\n  }\n}\n"
	writeFile(t, file, orig)
	h := newHarness(t, claudeCode{}, env)

	if _, err := h.a.Build(env, testInput(), nil); err == nil {
		t.Fatal("没有主模型时应当报错")
	}
	in := testInput()
	in.Default, in.Fast = "glm-5.2", "hy3"
	res := h.build(in)
	if len(res.Warnings) != 0 {
		t.Fatalf("不应有警告：%v", res.Warnings)
	}
	got := readFile(t, file)
	if !strings.Contains(got, "// 我的设置") {
		t.Fatal("注释丢失")
	}
	envMap := jsonMap(t, got)["env"].(map[string]any)
	if envMap["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:7863" || envMap["ANTHROPIC_AUTH_TOKEN"] != testKey ||
		envMap["ANTHROPIC_MODEL"] != "glm-5.2" || envMap["ANTHROPIC_SMALL_FAST_MODEL"] != "hy3" || envMap["KEEP"] != "1" {
		t.Fatalf("env 不符：%v", envMap)
	}
	if info := h.detect(Options{}); !info.Imported || info.ImportedModels[0] != "glm-5.2" {
		t.Fatalf("Detect = %+v", info)
	}

	// 再导入一次（换模型、去掉快速模型）：原值记录不能被覆盖成我们自己的值。
	in2 := testInput()
	in2.Default = "claude-sonnet-5"
	h.build(in2)
	envMap = jsonMap(t, readFile(t, file))["env"].(map[string]any)
	if envMap["ANTHROPIC_MODEL"] != "claude-sonnet-5" {
		t.Fatalf("模型未更新：%v", envMap)
	}
	if _, has := envMap["ANTHROPIC_SMALL_FAST_MODEL"]; has {
		t.Fatalf("不再需要的快速模型应被还原（原先不存在）：%v", envMap)
	}

	h.remove(Options{})
	if got := readFile(t, file); got != orig {
		t.Fatalf("移除后应还原\n想要：%q\n实际：%q", orig, got)
	}
}

func TestClaudeCodeWarnsAboutAPIKeyAndHelper(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".claude", "settings.json")
	writeFile(t, file, `{"env":{"ANTHROPIC_API_KEY":"x"},"apiKeyHelper":"echo"}`)
	in := testInput()
	in.Default = "glm-5.2"
	res, err := claudeCode{}.Build(env, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("应有 2 条警告：%v", res.Warnings)
	}
}

func TestClaudeCodeRemoveWithoutLedgerOnlyDeletesOurs(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".claude", "settings.json")
	writeFile(t, file, "{\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:7863\",\n    \"ANTHROPIC_AUTH_TOKEN\": \""+testKey+"\",\n    \"ANTHROPIC_MODEL\": \"glm-5.2\",\n    \"ANTHROPIC_SMALL_FAST_MODEL\": \"user-own\"\n  }\n}\n")
	res, err := claudeCode{}.Remove(env, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := string(res.Changes[0].After)
	if !strings.Contains(after, "user-own") || strings.Contains(after, "glm-5.2") {
		t.Fatalf("记录丢失时只能删确定是我们写的三项：\n%s", after)
	}
	other := testEnv(t)
	writeFile(t, filepath.Join(other.Home, ".claude", "settings.json"), `{"env":{"ANTHROPIC_AUTH_TOKEN":"someone-elses"}}`)
	if _, err := (claudeCode{}).Remove(other, Options{}, nil); err == nil {
		t.Fatal("不是我们写的配置不应被移除")
	}
}

// ---- Codex ----

const codexOrig = "# 我的 Codex 配置\nmodel = \"gpt-5\"\nmodel_reasoning_effort = \"high\"\n\n[profiles.fast]\nmodel = \"gpt-5-mini\"\n\n[mcp_servers.docs]\ncommand = \"npx\"\nargs = [\"-y\", \"docs\"]\n"

func TestCodexDefaultRestoreAndByteIdentical(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".codex", "config.toml")
	writeFile(t, file, codexOrig)
	h := newHarness(t, codex{}, env)

	in := testInput()
	in.Default = "glm-5.2"
	h.build(in)
	got := readFile(t, file)
	for _, want := range []string{"# 我的 Codex 配置", codexBegin, codexEnd, "[model_providers.poolservice]", `wire_api = "responses"`, `model = "glm-5.2"`, `model_provider = "poolservice"`, "[mcp_servers.docs]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q：\n%s", want, got)
		}
	}
	m, err := parseTOML(got)
	if err != nil {
		t.Fatal(err)
	}
	p := m["model_providers"].(map[string]any)["poolservice"].(map[string]any)
	if p["base_url"] != "http://127.0.0.1:7863/v1" || p["experimental_bearer_token"] != testKey {
		t.Fatalf("供应商不符：%v", p)
	}
	if m["profiles"].(map[string]any)["fast"].(map[string]any)["model"] != "gpt-5-mini" {
		t.Fatal("profile 内的 model 不应被改动")
	}
	if info := h.detect(Options{}); !info.Imported || info.ImportedModels[0] != "glm-5.2" {
		t.Fatalf("Detect = %+v", info)
	}

	// 再次写入应当幂等。
	h.build(in)
	if readFile(t, file) != got {
		t.Fatal("重复写入应当幂等")
	}

	h.remove(Options{})
	if got := readFile(t, file); got != codexOrig {
		t.Fatalf("移除后应与原文件逐字节一致\n想要：%q\n实际：%q", codexOrig, got)
	}
}

func TestCodexWithoutDefaultKeepsTopLevel(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".codex", "config.toml")
	writeFile(t, file, codexOrig)
	res, err := codex{}.Build(env, testInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("未设默认模型时应给出提示")
	}
	after := string(res.Changes[0].After)
	if !strings.HasPrefix(after, "# 我的 Codex 配置\nmodel = \"gpt-5\"") {
		t.Fatalf("顶层键不应被改动：\n%s", after)
	}
}

func TestCodexConflictCRLFAndBadToml(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".codex", "config.toml")
	writeFile(t, file, "[model_providers.poolservice]\nname = \"mine\"\n")
	if _, err := (codex{}).Build(env, testInput(), nil); err == nil {
		t.Fatal("已有同名的用户供应商应当拒绝")
	}

	crlf := strings.ReplaceAll(codexOrig, "\n", "\r\n")
	writeFile(t, file, crlf)
	in := testInput()
	in.Default = "glm-5.2"
	h := newHarness(t, codex{}, env)
	h.build(in)
	if got := readFile(t, file); strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("CRLF 文件出现裸 LF：%q", got)
	}
	h.remove(Options{})
	if got := readFile(t, file); got != crlf {
		t.Fatalf("CRLF 文件移除后应还原：%q", got)
	}

	writeFile(t, file, "model = = broken")
	if _, err := (codex{}).Build(env, testInput(), nil); err == nil {
		t.Fatal("不合法的 TOML 应当拒绝")
	}
}

func TestCodexFreshFile(t *testing.T) {
	env := testEnv(t)
	h := newHarness(t, codex{}, env)
	in := testInput()
	in.Default = "glm-5.2"
	h.build(in)
	file := filepath.Join(env.Home, ".codex", "config.toml")
	if _, err := parseTOML(readFile(t, file)); err != nil {
		t.Fatal(err)
	}
	h.remove(Options{})
	if got := readFile(t, file); strings.TrimSpace(got) != "" {
		t.Fatalf("新建的文件移除后应为空：%q", got)
	}
}

func TestCodexHomeEnv(t *testing.T) {
	env := testEnv(t, "CODEX_HOME", "{home}/custom-codex")
	if got := (codex{}).file(env, ""); got != filepath.Join(env.Home, "custom-codex", "config.toml") {
		t.Fatalf("CODEX_HOME 未生效：%s", got)
	}
}

// ---- OpenCode ----

const openCodeOrig = "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"provider\": {\n    \n    \"docode\": {\n      \"npm\": \"@ai-sdk/openai-compatible\",\n      \"options\": {\n        \"baseURL\": \"https://docode.cc/v1\",\n        \"apiKey\": \"k\"\n      }\n    }\n  }\n}\n"

func TestOpenCodeMergeDefaultAndRestore(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".config", "opencode", "opencode.json")
	writeFile(t, file, openCodeOrig)
	h := newHarness(t, openCode{}, env)
	in := testInput()
	in.Default = "glm-5.2"
	h.build(in)
	got := readFile(t, file)
	m := jsonMap(t, got)
	if m["model"] != "poolservice/glm-5.2" {
		t.Fatalf("默认模型：%v", m["model"])
	}
	prov := m["provider"].(map[string]any)
	if _, ok := prov["docode"]; !ok {
		t.Fatal("用户已有的供应商被破坏")
	}
	ours := prov["poolservice"].(map[string]any)
	opts := ours["options"].(map[string]any)
	if opts["baseURL"] != "http://127.0.0.1:7863/v1" || opts["apiKey"] != testKey || ours["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("供应商不符：%v", ours)
	}
	mods := ours["models"].(map[string]any)
	g := mods["glm-5.2"].(map[string]any)
	if g["limit"].(map[string]any)["context"].(float64) != 200000 || g["reasoning"] != true || g["tool_call"] != true {
		t.Fatalf("模型能力不符：%v", g)
	}
	if mods["claude-sonnet-5"].(map[string]any)["modalities"] == nil {
		t.Fatal("多模态模型应带 modalities")
	}
	h.build(in)
	if readFile(t, file) != got {
		t.Fatal("重复写入应当幂等")
	}
	h.remove(Options{})
	if got := readFile(t, file); got != openCodeOrig {
		t.Fatalf("移除后应还原\n想要：%q\n实际：%q", openCodeOrig, got)
	}
}

func TestOpenCodeFileSelectionAndConflict(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(env.Home, ".config", "opencode")
	writeFile(t, filepath.Join(dir, "opencode.jsonc"), "{\n  // 注释\n}\n")
	if got := (openCode{}).file(env, ""); filepath.Base(got) != "opencode.jsonc" {
		t.Fatalf("只有 jsonc 时应选它：%s", got)
	}
	writeFile(t, filepath.Join(dir, "opencode.json"), "{}")
	if got := (openCode{}).file(env, ""); filepath.Base(got) != "opencode.json" {
		t.Fatalf("两者都有时应选 json：%s", got)
	}
	env2 := testEnv(t, "OPENCODE_CONFIG", "{home}/x/oc.jsonc")
	if got := (openCode{}).file(env2, ""); got != filepath.Join(env2.Home, "x", "oc.jsonc") {
		t.Fatalf("OPENCODE_CONFIG 未生效：%s", got)
	}

	writeFile(t, filepath.Join(dir, "opencode.json"), `{"provider":{"poolservice":{"name":"别人的"}}}`)
	if _, err := (openCode{}).Build(env, testInput(), nil); err == nil {
		t.Fatal("已有同名的用户供应商应当拒绝")
	}
	writeFile(t, filepath.Join(dir, "opencode.json"), `{"providers":{"x":{}}}`)
	if _, err := (openCode{}).Build(env, testInput(), nil); err == nil {
		t.Fatal("新版 providers 结构应当拒绝")
	}
}

func TestOpenCodeFreshFile(t *testing.T) {
	env := testEnv(t)
	h := newHarness(t, openCode{}, env)
	h.build(testInput())
	file := filepath.Join(env.Home, ".config", "opencode", "opencode.json")
	m := jsonMap(t, readFile(t, file))
	if m["$schema"] != "https://opencode.ai/config.json" {
		t.Fatalf("新建文件应带 $schema：%v", m)
	}
	h.remove(Options{})
	if m := jsonMap(t, readFile(t, file)); len(m) != 1 {
		t.Fatalf("移除后只应剩 $schema：%v", m)
	}
}

// ---- Pi ----

func TestPiMergeConflictRemove(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".pi", "agent", "models.json")
	orig := "{\n  \"providers\": {\n    \"mine\": {\n      \"baseUrl\": \"http://x\",\n      \"models\": [{ \"id\": \"m\" }]\n    }\n  }\n}\n"
	writeFile(t, file, orig)
	h := newHarness(t, piAgent{}, env)
	h.build(testInput())
	m := jsonMap(t, readFile(t, file))
	prov := m["providers"].(map[string]any)
	ours := prov["poolservice"].(map[string]any)
	if ours["baseUrl"] != "http://127.0.0.1:7863/v1" || ours["api"] != "openai-completions" || ours["apiKey"] != testKey {
		t.Fatalf("供应商不符：%v", ours)
	}
	first := ours["models"].([]any)[0].(map[string]any)
	tl := first["thinkingLevelMap"].(map[string]any)
	if tl["high"] != "high" || tl["xhigh"] != nil || tl["off"] != nil {
		t.Fatalf("thinkingLevelMap 不符：%v", tl)
	}
	if _, ok := ours["models"].([]any)[2].(map[string]any)["thinkingLevelMap"]; ok {
		t.Fatal("无思考档位的模型不应写 thinkingLevelMap")
	}
	h.remove(Options{})
	if got := readFile(t, file); got != orig {
		t.Fatalf("移除后应还原\n想要：%q\n实际：%q", orig, got)
	}

	writeFile(t, file, `{"providers":{"poolservice":{"name":"别人的"}}}`)
	if _, err := (piAgent{}).Build(env, testInput(), nil); err == nil {
		t.Fatal("已有同名的用户供应商应当拒绝")
	}
}

func TestPiAgentDirEnv(t *testing.T) {
	env := testEnv(t, "PI_CODING_AGENT_DIR", "{home}/pi-custom")
	if got := (piAgent{}).file(env, ""); got != filepath.Join(env.Home, "pi-custom", "models.json") {
		t.Fatalf("PI_CODING_AGENT_DIR 未生效：%s", got)
	}
}

// ---- OMP ----

func TestOMPYamlKeepsCommentsAndRestores(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".omp", "agent", "models.yml")
	orig := "# 我的 OMP 配置\nfuture:\n  enabled: true # 原生扩展\nproviders:\n  external:\n    api: openai-responses # keep\n    models:\n      - id: ext\n"
	writeFile(t, file, orig)
	h := newHarness(t, ompAgent{}, env)
	h.build(testInput())
	got := readFile(t, file)
	for _, want := range []string{"# 我的 OMP 配置", "# 原生扩展", "# keep", ompMark} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q：\n%s", want, got)
		}
	}
	v := yamlAny(t, got).(map[string]any)
	ours := v["providers"].(map[string]any)["poolservice"].(map[string]any)
	if ours["baseUrl"] != "http://127.0.0.1:7863/v1" || ours["apiKey"] != testKey {
		t.Fatalf("供应商不符：%v", ours)
	}
	g := ours["models"].([]any)[0].(map[string]any)
	if g["contextWindow"] != 200000 || g["thinking"].(map[string]any)["defaultLevel"] != "high" {
		t.Fatalf("模型不符：%v", g)
	}
	if info := h.detect(Options{}); !info.Imported || len(info.ImportedModels) != 3 {
		t.Fatalf("Detect = %+v", info)
	}
	// 更新幂等
	h.build(testInput())
	if readFile(t, file) != got {
		t.Fatal("重复写入应当幂等")
	}
	h.remove(Options{})
	after := readFile(t, file)
	if !reflect.DeepEqual(yamlAny(t, after), yamlAny(t, orig)) {
		t.Fatalf("移除后语义应与原文件一致\n%s", after)
	}
	if !strings.Contains(after, "# 我的 OMP 配置") || !strings.Contains(after, "# 原生扩展") {
		t.Fatalf("移除后注释应保留：\n%s", after)
	}
}

func TestOMPProfilesLegacyAndConflict(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.Home, ".omp", "profiles", "work", "agent", "models.yml"), "providers: {}\n")
	info := (ompAgent{}).Detect(env, Options{}, nil)
	if len(info.Profiles) != 2 || info.Profiles[1].ID != "work" {
		t.Fatalf("应列出档案：%+v", info.Profiles)
	}
	h := newHarness(t, ompAgent{}, env)
	in := testInput()
	in.Profile = "work"
	h.build(in)
	if !strings.Contains(readFile(t, filepath.Join(env.Home, ".omp", "profiles", "work", "agent", "models.yml")), "poolservice") {
		t.Fatal("应写入所选档案")
	}
	if exists(filepath.Join(env.Home, ".omp", "agent", "models.yml")) {
		t.Fatal("不应写到默认目录")
	}
	bad := testInput()
	bad.Profile = "../escape"
	if _, err := (ompAgent{}).Build(env, bad, nil); err == nil {
		t.Fatal("非法档案名应当拒绝")
	}

	env2 := testEnv(t)
	writeFile(t, filepath.Join(env2.Home, ".omp", "agent", "models.json"), `{"providers":{}}`)
	if _, err := (ompAgent{}).Build(env2, testInput(), nil); err == nil {
		t.Fatal("只有旧版 models.json 时应要求先迁移")
	}
	env3 := testEnv(t)
	writeFile(t, filepath.Join(env3.Home, ".omp", "agent", "models.yml"), "providers:\n  poolservice:\n    api: x\n")
	if _, err := (ompAgent{}).Build(env3, testInput(), nil); err == nil {
		t.Fatal("已有同名的用户供应商应当拒绝")
	}
	env4 := testEnv(t)
	writeFile(t, filepath.Join(env4.Home, ".omp", "agent", "models.yml"), "providers: {}\n---\nproviders: {}\n")
	if _, err := (ompAgent{}).Build(env4, testInput(), nil); err == nil {
		t.Fatal("多文档 YAML 应当拒绝")
	}
}

func TestOMPEnvResolution(t *testing.T) {
	env := testEnv(t, "PI_CONFIG_DIR", "{home}/cfgroot", "OMP_PROFILE", "dev")
	dir, err := (ompAgent{}).agentDir(env, Options{})
	if err != nil || dir != filepath.Join(env.Home, "cfgroot", "profiles", "dev", "agent") {
		t.Fatalf("OMP_PROFILE：%s %v", dir, err)
	}
	env2 := testEnv(t, "PI_CODING_AGENT_DIR", "{home}/direct")
	dir, _ = (ompAgent{}).agentDir(env2, Options{})
	if dir != filepath.Join(env2.Home, "direct") {
		t.Fatalf("PI_CODING_AGENT_DIR：%s", dir)
	}
}

// ---- DeepSeek Harness ----

const dshPatchOrig = `# patch layer
- id: ui-settings-general
  name: "@deepseek-ai/dsh-client-ui-settings-general"
  config:
    welcomeNoticeVersion: 2026-08-13.1
- id: llm-pi-ai
  name: "@deepseek-ai/dsh-llm-pi-ai"
  config:
    providers:
      other-provider:
        displayName: Other
        apiKeyEnv: OTHER_KEY
        baseURL: http://other/v1
        models:
          - id: a
- id: agent-default-model
  name: "@deepseek-ai/dsh-agent-default-model"
  config:
    provider: other-provider
    model: a
    reasoningEffort: high
- insert:
    - id: mcp-x
`

const dshCredOrig = "version: 1\nrefs:\n  DEEPSEEK_API_KEY: user-secret\nrecords:\n  a/b:\n    kind: x\n"

func TestDSHProfileModeWithDefaultAndRestore(t *testing.T) {
	env := testEnv(t)
	home := filepath.Join(env.Home, ".dsh")
	patch := filepath.Join(home, "profiles", "desktop", dshPatchFile)
	writeFile(t, patch, dshPatchOrig)
	writeFile(t, filepath.Join(home, "profiles", "web", dshPatchFile), "[]\n")
	writeFile(t, filepath.Join(home, ".credentials.yaml"), dshCredOrig)
	h := newHarness(t, dshAgent{}, env)

	info := h.detect(Options{})
	if info.Profile != "desktop" || len(info.Profiles) != 2 || !info.SupportsDefault {
		t.Fatalf("Detect 应默认选中已有 llm-pi-ai 的 desktop 档案：%+v", info)
	}

	in := testInput()
	in.Default = "glm-5.2"
	h.build(in)
	got := readFile(t, patch)
	if !strings.Contains(got, "# patch layer") || !strings.Contains(got, "other-provider") || !strings.Contains(got, "mcp-x") {
		t.Fatalf("原有内容被破坏：\n%s", got)
	}
	seq := yamlAny(t, got).([]any)
	var llm, def map[string]any
	for _, it := range seq {
		m := it.(map[string]any)
		switch m["id"] {
		case "llm-pi-ai":
			llm = m
		case "agent-default-model":
			def = m
		}
	}
	prov := llm["config"].(map[string]any)["providers"].(map[string]any)
	ours := prov["poolservice"].(map[string]any)
	if ours["apiKeyEnv"] != dshEnvName || ours["baseURL"] != "http://127.0.0.1:7863/v1" || ours["api"] != "openai-completions" {
		t.Fatalf("供应商不符：%v", ours)
	}
	if _, ok := ours["apiKey"]; ok {
		t.Fatal("密钥不应直接写进供应商")
	}
	gm := ours["models"].([]any)[0].(map[string]any)
	if gm["contextWindow"] != 200000 || gm["reasoningEfforts"].(map[string]any)["high"] != "high" {
		t.Fatalf("模型不符：%v", gm)
	}
	cfg := def["config"].(map[string]any)
	if cfg["provider"] != "poolservice" || cfg["model"] != "glm-5.2" || cfg["reasoningEffort"] != "high" {
		t.Fatalf("默认模型不符：%v", cfg)
	}
	cred := yamlAny(t, readFile(t, filepath.Join(home, ".credentials.yaml"))).(map[string]any)
	refs := cred["refs"].(map[string]any)
	if refs[dshEnvName] != testKey || refs["DEEPSEEK_API_KEY"] != "user-secret" || cred["records"] == nil {
		t.Fatalf("凭据文件不符：%v", cred)
	}
	if info := h.detect(Options{}); !info.Imported || info.Profile != "desktop" {
		t.Fatalf("Detect = %+v", info)
	}

	h.build(in)
	if readFile(t, patch) != got {
		t.Fatal("重复写入应当幂等")
	}

	h.remove(Options{})
	if !reflect.DeepEqual(yamlAny(t, readFile(t, patch)), yamlAny(t, dshPatchOrig)) {
		t.Fatalf("移除后语义应与原文件一致\n%s", readFile(t, patch))
	}
	if !reflect.DeepEqual(yamlAny(t, readFile(t, filepath.Join(home, ".credentials.yaml"))), yamlAny(t, dshCredOrig)) {
		t.Fatalf("移除后凭据文件应还原\n%s", readFile(t, filepath.Join(home, ".credentials.yaml")))
	}
}

func TestDSHEmptyProfileAndCreatedDefaultEntryRemoved(t *testing.T) {
	env := testEnv(t)
	home := filepath.Join(env.Home, ".dsh")
	patch := filepath.Join(home, "profiles", "web", dshPatchFile)
	writeFile(t, patch, "# Your patch layer\n[]\n")
	h := newHarness(t, dshAgent{}, env)
	in := testInput()
	in.Default = "hy3"
	h.build(in)
	got := readFile(t, patch)
	if strings.Contains(got, "[]") {
		t.Fatalf("空数组写入条目后不应再是流式写法：\n%s", got)
	}
	if !strings.Contains(got, "# Your patch layer") {
		t.Fatalf("头部注释丢失：\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".credentials.yaml")); err != nil {
		t.Fatal("凭据文件不存在时应新建")
	}
	cred := yamlAny(t, readFile(t, filepath.Join(home, ".credentials.yaml"))).(map[string]any)
	if cred["refs"].(map[string]any)[dshEnvName] != testKey {
		t.Fatalf("新建凭据不符：%v", cred)
	}
	h.remove(Options{})
	seq, _ := yamlAny(t, readFile(t, patch)).([]any)
	for _, it := range seq {
		if it.(map[string]any)["id"] == "agent-default-model" {
			t.Fatal("本工具新建的 agent-default-model 条目移除后应删除")
		}
	}
}

func TestDSHLegacySettingsAndGuards(t *testing.T) {
	env := testEnv(t)
	home := filepath.Join(env.Home, ".dsh")
	writeFile(t, filepath.Join(home, "settings.yaml"), "other: 1\n")
	h := newHarness(t, dshAgent{}, env)
	info := h.detect(Options{})
	if info.SupportsDefault || len(info.Profiles) != 0 {
		t.Fatalf("旧版布局不支持默认模型：%+v", info)
	}
	in := testInput()
	in.Default = "glm-5.2"
	h.build(in)
	v := yamlAny(t, readFile(t, filepath.Join(home, "settings.yaml"))).(map[string]any)
	prov := v["llm-pi-ai"].(map[string]any)["providers"].(map[string]any)
	if prov["poolservice"] == nil || v["other"] != 1 {
		t.Fatalf("旧版写入不符：%v", v)
	}
	h.remove(Options{})
	if !reflect.DeepEqual(yamlAny(t, readFile(t, filepath.Join(home, "settings.yaml"))), yamlAny(t, "other: 1\n")) {
		t.Fatalf("旧版移除后应还原：%s", readFile(t, filepath.Join(home, "settings.yaml")))
	}

	bad := testInput()
	bad.Profile = "nope"
	env2 := testEnv(t)
	writeFile(t, filepath.Join(env2.Home, ".dsh", "profiles", "web", dshPatchFile), "[]\n")
	if _, err := (dshAgent{}).Build(env2, bad, nil); err == nil {
		t.Fatal("不存在的档案应当拒绝")
	}
	env3 := testEnv(t)
	writeFile(t, filepath.Join(env3.Home, ".dsh", "profiles", "web", dshPatchFile), "- id: llm-pi-ai\n  config:\n    providers:\n      poolservice:\n        apiKeyEnv: MINE\n")
	if _, err := (dshAgent{}).Build(env3, testInput(), nil); err == nil {
		t.Fatal("已有同名的用户供应商应当拒绝")
	}
	env4 := testEnv(t)
	writeFile(t, filepath.Join(env4.Home, ".dsh", "profiles", "web", dshPatchFile), "[]\n")
	writeFile(t, filepath.Join(env4.Home, ".dsh", ".credentials.yaml"), "version: 2\nrefs: {}\n")
	if _, err := (dshAgent{}).Build(env4, testInput(), nil); err == nil {
		t.Fatal("无法识别的凭据文件布局应当拒绝")
	}
}

func TestDSHHomeEnvAndFlatCredentials(t *testing.T) {
	env := testEnv(t, "DSH_HOME", "{home}/elsewhere")
	home := filepath.Join(env.Home, "elsewhere")
	writeFile(t, filepath.Join(home, "profiles", "web", dshPatchFile), "[]\n")
	writeFile(t, filepath.Join(home, ".credentials.yaml"), "SOME_KEY: v\n")
	h := newHarness(t, dshAgent{}, env)
	h.build(testInput())
	cred := yamlAny(t, readFile(t, filepath.Join(home, ".credentials.yaml"))).(map[string]any)
	if cred[dshEnvName] != testKey || cred["SOME_KEY"] != "v" {
		t.Fatalf("扁平布局应直接写在根上：%v", cred)
	}
}

// ---- Claude Desktop ----

func desktopEnv(t *testing.T) (Env, cdPaths) {
	env := testEnv(t)
	env.GOOS = "windows"
	p, err := (claudeDesktop{}).paths(env, "")
	if err != nil {
		t.Fatal(err)
	}
	return env, p
}

func TestClaudeDesktopTakeoverAndRestore(t *testing.T) {
	env, p := desktopEnv(t)
	normalOrig := "{\n  \"deploymentMode\": \"3p\"\n}\n"
	threepOrig := "{\n  \"mcpServers\": {},\n  \"deploymentMode\": \"1p\"\n}\n"
	metaOrig := "{\"appliedId\": \"other-id\", \"entries\": [\n    {\n      \"id\": \"other-id\",\n      \"name\": \"TT Switch\"\n    }\n  ]}"
	writeFile(t, p.normalCfg, normalOrig)
	writeFile(t, p.threepCfg, threepOrig)
	writeFile(t, p.meta, metaOrig)
	h := newHarness(t, claudeDesktop{}, env)

	info := h.detect(Options{})
	if !info.Installed || info.Blocked != "" || !strings.Contains(strings.Join(info.Notes, "|"), "TT Switch") {
		t.Fatalf("Detect = %+v", info)
	}

	res := h.build(testInput())
	if !reflect.DeepEqual(res.Imported, []string{"claude-sonnet-5"}) {
		t.Fatalf("只有 claude-* 命名的模型能导入：%v", res.Imported)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("其余模型应被跳过：%+v", res.Skipped)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("接管另一份启用中的配置时应有警告")
	}
	prof := jsonMap(t, readFile(t, p.profile))
	if prof["inferenceGatewayBaseUrl"] != "http://127.0.0.1:7863" || prof["inferenceGatewayApiKey"] != testKey || prof["inferenceProvider"] != "gateway" {
		t.Fatalf("档案不符：%v", prof)
	}
	im := prof["inferenceModels"].([]any)[0].(map[string]any)
	if im["name"] != "claude-sonnet-5" || im["anthropicFamilyTier"] != "sonnet" || im["isFamilyDefault"] != true || im["supports1m"] != true {
		t.Fatalf("模型不符：%v", im)
	}
	meta := jsonMap(t, readFile(t, p.meta))
	if meta["appliedId"] != cdProfileID || len(meta["entries"].([]any)) != 2 {
		t.Fatalf("meta 不符：%v", meta)
	}
	if jsonMap(t, readFile(t, p.threepCfg))["deploymentMode"] != "3p" {
		t.Fatal("3p 配置应切到 3p")
	}
	if got := readFile(t, p.threepCfg); !strings.Contains(got, "mcpServers") {
		t.Fatalf("其余配置被破坏：%s", got)
	}

	h.build(testInput())
	h.remove(Options{})
	if exists(p.profile) {
		t.Fatal("移除后档案应删除")
	}
	if got := readFile(t, p.meta); got != metaOrig {
		t.Fatalf("meta 应还原\n想要：%q\n实际：%q", metaOrig, got)
	}
	if got := readFile(t, p.threepCfg); got != threepOrig {
		t.Fatalf("3p 配置应还原\n想要：%q\n实际：%q", threepOrig, got)
	}
	if got := readFile(t, p.normalCfg); got != normalOrig {
		t.Fatalf("普通配置应还原：%q", got)
	}
}

func TestClaudeDesktopGuards(t *testing.T) {
	env, p := desktopEnv(t)
	if info := (claudeDesktop{}).Detect(env, Options{}, nil); info.Blocked == "" {
		t.Fatal("没装 Claude Desktop 时应给出 Blocked")
	}
	if _, err := (claudeDesktop{}).Build(env, testInput(), nil); err == nil {
		t.Fatal("没装 Claude Desktop 时应当拒绝")
	}
	writeFile(t, p.threepCfg, "{}")
	if _, err := (claudeDesktop{}).Build(env, testInput(testModels[0], testModels[2]), nil); err == nil {
		t.Fatal("没有 claude-* 命名的模型时应当拒绝")
	}
	env.GOOS = "linux"
	if _, err := (claudeDesktop{}).Build(env, testInput(), nil); err == nil {
		t.Fatal("Linux 暂不支持")
	}
}

func TestClaudeFamily(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-5":          "sonnet",
		"claude-opus-4.8-1m":       "opus",
		"anthropic/claude-haiku-4": "haiku",
		"claude-sonnet-":           "",
		"claude-sonnet":            "",
		"glm-5.2":                  "",
		"claude-sonnet-4[1m]":      "",
		"claude-sonnet-5(x1.5)":    "",
		"claude-instant-1":         "",
		"  Claude-Sonnet-5  ":      "sonnet",
	}
	for in, want := range cases {
		if got := claudeFamily(in); got != want {
			t.Errorf("claudeFamily(%q) = %q，想要 %q", in, got, want)
		}
	}
}

// ---- Manager ----

func TestManagerRejectsConcurrentModification(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.Home, ".workbuddy", "models.json")
	writeFile(t, file, "{\"models\": []}\n")
	a := modelsJSON{id: "workbuddy", name: "WorkBuddy", dirName: ".workbuddy"}
	mgr := NewManager(t.TempDir())
	res, err := a.Build(env, testInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, "{\"models\": [], \"changed\": true}\n")
	if _, err := mgr.Apply(a.ID(), res); !errors.Is(err, ErrChanged) {
		t.Fatalf("文件在预览后被改动应返回 ErrChanged，实际：%v", err)
	}
	if readFile(t, file) != "{\"models\": [], \"changed\": true}\n" {
		t.Fatal("拒绝时不应改动文件")
	}
}

func TestManagerRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	writeFile(t, good, "old")
	blocker := filepath.Join(dir, "blocker")
	writeFile(t, blocker, "i am a file")
	bad := filepath.Join(blocker, "sub", "x.json") // 父路径是文件，必然写入失败
	mgr := NewManager(t.TempDir())
	res := &Result{Changes: []Change{
		{Path: good, Before: []byte("old"), Existed: true, After: []byte("new")},
		{Path: bad, After: []byte("x")},
	}}
	if _, err := mgr.Apply("t", res); err == nil {
		t.Fatal("应当失败")
	}
	if readFile(t, good) != "old" {
		t.Fatalf("失败后已写的文件应还原，实际：%q", readFile(t, good))
	}
}

func TestManagerBackupAndPrune(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.json")
	writeFile(t, f, "v0")
	mgr := NewManager(t.TempDir())
	mgr.now = fakeClock(0)
	cur := "v0"
	for i := 1; i <= keepBackups+3; i++ {
		next := "v" + string(rune('0'+i%10)) + strings.Repeat("x", i)
		res := &Result{Changes: []Change{{Path: f, Before: []byte(cur), Existed: true, After: []byte(next)}}}
		out, err := mgr.Apply("t", res)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			b, err := os.ReadFile(filepath.Join(out.BackupDir, safeName(f)))
			if err != nil || string(b) != "v0" {
				t.Fatalf("备份内容不符：%q %v", b, err)
			}
		}
		cur = next
		mgr.now = fakeClock(i + 1)
	}
	ents, _ := os.ReadDir(filepath.Join(mgr.Dir, "agent-backups", "t"))
	if len(ents) != keepBackups {
		t.Fatalf("备份应保留最近 %d 份，实际 %d", keepBackups, len(ents))
	}
}

func TestManagerRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	writeFile(t, target, "{}")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("当前环境不能创建符号链接：%v", err)
	}
	if _, _, err := readOpt(link); err == nil {
		t.Fatal("符号链接应当拒绝")
	}
}

func TestPrevPersistence(t *testing.T) {
	mgr := NewManager(t.TempDir())
	prev := Prev{"a": {Present: true, Raw: json.RawMessage(`"x"`)}, "b": {Present: false}}
	res := &Result{Prev: prev}
	if _, err := mgr.Apply("agent", res); err != nil {
		t.Fatal(err)
	}
	got := mgr.LoadPrev("agent")
	if !reflect.DeepEqual(got, prev) {
		t.Fatalf("还原记录读回不一致：%v", got)
	}
	if _, err := mgr.Apply("agent", &Result{ClearPrev: true}); err != nil {
		t.Fatal(err)
	}
	if mgr.LoadPrev("agent") != nil {
		t.Fatal("ClearPrev 后应清除")
	}
}

// ---- 通用 ----

func TestDetectOnEmptyHomeNeverPanics(t *testing.T) {
	env := testEnv(t)
	for _, a := range All() {
		info := a.Detect(env, Options{}, nil)
		if info.ID != a.ID() || info.Name == "" || info.Protocol == "" {
			t.Errorf("%s: Info 不完整：%+v", a.ID(), info)
		}
		if info.Imported {
			t.Errorf("%s: 空环境不应显示已导入", a.ID())
		}
	}
}

func TestAllIDsUniqueAndFindable(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range All() {
		if seen[a.ID()] {
			t.Fatalf("重复的 ID：%s", a.ID())
		}
		seen[a.ID()] = true
		if Find(a.ID()) == nil {
			t.Fatalf("Find(%s) 为空", a.ID())
		}
	}
	if Find("nope") != nil {
		t.Fatal("未知 ID 应返回 nil")
	}
}

func TestMaskSecret(t *testing.T) {
	m := MaskSecret(testKey)
	if strings.Contains(m, testKey[10:30]) || !strings.HasPrefix(m, "sk-pool-") {
		t.Fatalf("遮掩不当：%s", m)
	}
	if MaskSecret("short") != "•••••" {
		t.Fatal("短密钥应全部遮掩")
	}
}
