package panelapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/agentcfg"
	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
	"github.com/orangeruan/poolservice/internal/store/keys"
)

// fakeChat 提供固定的模型目录。
type fakeChat struct{ plainProvider }

func (fakeChat) Models(context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{ID: "glm-5.2", DisplayName: "GLM 5.2", ContextWindow: 200000, MaxOutputTokens: 32000, SupportsTools: true, SupportsThinking: true, ReasoningLevels: []string{"low", "high"}, CreditRate: 1},
		{ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", ContextWindow: 1000000, MaxOutputTokens: 64000, SupportsImages: true, SupportsTools: true, CreditRate: 2},
		{ID: "hy3", DisplayName: "hy3", CreditRate: 0},
	}, nil
}

func (fakeChat) ServeChat(context.Context, *provider.ChatRequest, http.ResponseWriter) provider.ChatOutcome {
	return provider.ChatOutcome{}
}

type agentEnvT struct {
	api  *API
	home string
	cfg  *config.Config
	ks   *keys.Store
}

func newAgentAPI(t *testing.T, adminKey string) *agentEnvT {
	t.Helper()
	reg := provider.NewRegistry()
	if err := reg.Register(fakeChat{plainProvider{id: "wb"}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ks, err := keys.Open(filepath.Join(dir, "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminKey: adminKey, DataDir: filepath.Join(dir, "data")}
	cfg.Gateway.ModelNaming = "short"
	api := New(func() *config.Config { return cfg }, nil, reg, router.NewCatalog(reg, 0))
	api.SetStores(ks, nil)
	home := t.TempDir()
	api.SetAgentEnv(agentcfg.Env{
		GOOS: runtime.GOOS, Home: home,
		AppData: filepath.Join(home, "AppData", "Roaming"), LocalAppData: filepath.Join(home, "AppData", "Local"),
		Getenv: func(string) string { return "" },
	})
	return &agentEnvT{api: api, home: home, cfg: cfg, ks: ks}
}

type reqOpt struct {
	remote bool
	origin string
	noAuth bool
}

func (e *agentEnvT) do(t *testing.T, method, path, body string, o reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	e.api.Register(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:7863"
	if o.remote {
		req.RemoteAddr = "192.168.1.9:50000"
	}
	if o.origin != "" {
		req.Header.Set("Origin", o.origin)
	}
	if !o.noAuth && e.cfg.AdminKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.AdminKey)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("响应不是预期的 JSON：%v\n%s", err, rec.Body.String())
	}
	return v
}

func (e *agentEnvT) wbFile() string {
	return filepath.Join(e.home, ".workbuddy", "models.json")
}

func TestAgentsListShape(t *testing.T) {
	e := newAgentAPI(t, "adm")
	rec := e.do(t, "GET", "/api/agents", "", reqOpt{})
	if rec.Code != 200 {
		t.Fatalf("code=%d %s", rec.Code, rec.Body.String())
	}
	out := decode[struct {
		Agents   []agentcfg.Info `json:"agents"`
		Models   []agentModel    `json:"models"`
		CanWrite bool            `json:"can_write"`
		NoKeys   bool            `json:"no_keys"`
		BaseURL  string          `json:"base_url"`
	}](t, rec)
	if len(out.Agents) != 9 || len(out.Models) != 3 || !out.CanWrite || !out.NoKeys {
		t.Fatalf("agents=%d models=%d can_write=%v no_keys=%v", len(out.Agents), len(out.Models), out.CanWrite, out.NoKeys)
	}
	if out.BaseURL != "http://127.0.0.1:7863" {
		t.Fatalf("base_url=%s", out.BaseURL)
	}
	var glm agentModel
	for _, m := range out.Models {
		if m.ID == "glm-5.2" {
			glm = m
		}
	}
	if glm.Context != 200000 || !glm.Reasoning || len(glm.Efforts) != 2 {
		t.Fatalf("模型字段不符：%+v", glm)
	}
	if rec := e.do(t, "GET", "/api/agents", "", reqOpt{noAuth: true}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺少管理密钥应 401：%d", rec.Code)
	}
}

func TestAgentApplyGuards(t *testing.T) {
	body := `{"models":["glm-5.2"]}`
	t.Run("没有设置 admin_key", func(t *testing.T) {
		e := newAgentAPI(t, "")
		if rec := e.do(t, "POST", "/api/agents/workbuddy/apply", body, reqOpt{}); rec.Code != http.StatusForbidden {
			t.Fatalf("code=%d", rec.Code)
		}
		if rec := e.do(t, "POST", "/api/agents/workbuddy/preview", body, reqOpt{}); rec.Code != 200 {
			t.Fatalf("预览不受限制：%d %s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(e.wbFile()); err == nil {
			t.Fatal("被拒绝时不应写文件")
		}
	})
	t.Run("非本机请求", func(t *testing.T) {
		e := newAgentAPI(t, "adm")
		rec := e.do(t, "POST", "/api/agents/workbuddy/apply", body, reqOpt{remote: true})
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "本机") {
			t.Fatalf("code=%d %s", rec.Code, rec.Body.String())
		}
		if rec := e.do(t, "POST", "/api/agents/workbuddy/render", body, reqOpt{remote: true}); rec.Code != 400 {
			t.Fatalf("没有密钥时渲染应提示先创建密钥：%d", rec.Code)
		}
	})
	t.Run("跨站请求", func(t *testing.T) {
		e := newAgentAPI(t, "adm")
		if rec := e.do(t, "POST", "/api/agents/workbuddy/apply", body, reqOpt{origin: "http://evil.example"}); rec.Code != http.StatusForbidden {
			t.Fatalf("code=%d", rec.Code)
		}
		if rec := e.do(t, "POST", "/api/agents/workbuddy/apply", body, reqOpt{origin: "http://127.0.0.1:7863"}); rec.Code != 200 {
			t.Fatalf("同源应放行：%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("转发头", func(t *testing.T) {
		e := newAgentAPI(t, "adm")
		mux := http.NewServeMux()
		e.api.Register(mux)
		req := httptest.NewRequest("POST", "/api/agents/workbuddy/apply", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("Authorization", "Bearer adm")
		req.Header.Set("X-Forwarded-For", "8.8.8.8")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("反向代理转发来的请求不能当作本机：%d", rec.Code)
		}
	})
	t.Run("未知 Agent", func(t *testing.T) {
		e := newAgentAPI(t, "adm")
		if rec := e.do(t, "POST", "/api/agents/nope/apply", body, reqOpt{}); rec.Code != http.StatusNotFound {
			t.Fatalf("code=%d", rec.Code)
		}
	})
}

func TestAgentApplyCreatesDefaultKeyOnlyWhenNoneExist(t *testing.T) {
	e := newAgentAPI(t, "adm")
	rec := e.do(t, "POST", "/api/agents/workbuddy/preview", `{"models":["glm-5.2"]}`, reqOpt{})
	plan := decode[agentPlan](t, rec)
	if rec.Code != 200 || plan.Key == nil || !plan.Key.WillNew {
		t.Fatalf("没有任何密钥时预览应提示将创建默认密钥：%d %+v", rec.Code, plan.Key)
	}
	if n := len(e.ks.List()); n != 0 {
		t.Fatalf("预览不应创建密钥，实际 %d 把", n)
	}

	rec = e.do(t, "POST", "/api/agents/workbuddy/apply", `{"models":["glm-5.2","claude-sonnet-5"]}`, reqOpt{})
	if rec.Code != 200 {
		t.Fatalf("code=%d %s", rec.Code, rec.Body.String())
	}
	plan = decode[agentPlan](t, rec)
	list := e.ks.List()
	if len(list) != 1 || !plan.Key.Created || plan.Key.Name != defaultKeyName || len(list[0].Models) != 0 || list[0].RPM != 0 {
		t.Fatalf("应创建一把不限模型的默认密钥：%+v %+v", plan.Key, list)
	}
	written := readTestFile(t, e.wbFile())
	if !strings.Contains(written, list[0].Secret) || !strings.Contains(written, "http://127.0.0.1:7863/v1/chat/completions") {
		t.Fatalf("写入的文件应带默认密钥与网关地址：\n%s", written)
	}
	if strings.Contains(rec.Body.String(), list[0].Secret) {
		t.Fatal("响应里不应出现密钥明文")
	}

	// 已有密钥后，不指定密钥必须被拒绝，而不是再悄悄建一把。
	rec = e.do(t, "POST", "/api/agents/codebuddy/apply", `{"models":["glm-5.2"]}`, reqOpt{})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "选择") {
		t.Fatalf("已有密钥时应要求选择：%d %s", rec.Code, rec.Body.String())
	}
	if n := len(e.ks.List()); n != 1 {
		t.Fatalf("不应再创建密钥：%d", n)
	}
}

func TestAgentApplyUsesChosenKeyAndWhitelist(t *testing.T) {
	e := newAgentAPI(t, "adm")
	limited, err := e.ks.Create(keys.Spec{Name: "只给 glm", Models: []string{"glm-*"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	off := false
	disabled, _ := e.ks.Create(keys.Spec{Name: "停用的", Enabled: &off}, "")

	rec := e.do(t, "POST", "/api/agents/workbuddy/apply", `{"key_id":"`+disabled.ID+`","models":["glm-5.2"]}`, reqOpt{})
	if rec.Code != 400 {
		t.Fatalf("停用的密钥应拒绝：%d", rec.Code)
	}
	rec = e.do(t, "POST", "/api/agents/workbuddy/apply", `{"key_id":"`+limited.ID+`","models":["glm-5.2","hy3","ghost"]}`, reqOpt{})
	if rec.Code != 200 {
		t.Fatalf("code=%d %s", rec.Code, rec.Body.String())
	}
	plan := decode[agentPlan](t, rec)
	if len(plan.Imported) != 1 || plan.Imported[0] != "glm-5.2" {
		t.Fatalf("白名单外的模型不应导入：%+v", plan.Imported)
	}
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		reasons[s.ID] = s.Reason
	}
	if !strings.Contains(reasons["hy3"], "权限") || !strings.Contains(reasons["ghost"], "目录") {
		t.Fatalf("跳过原因不符：%v", reasons)
	}
	if !strings.Contains(readTestFile(t, e.wbFile()), limited.Secret) {
		t.Fatal("应写入所选密钥")
	}

	// 模型列表按所选密钥过滤。
	list := decode[struct{ Models []agentModel }](t, e.do(t, "GET", "/api/agents?key_id="+limited.ID, "", reqOpt{}))
	if len(list.Models) != 1 || list.Models[0].ID != "glm-5.2" {
		t.Fatalf("按密钥过滤的模型列表不符：%+v", list.Models)
	}
}

func TestAgentPreviewMasksButRenderDoesNot(t *testing.T) {
	e := newAgentAPI(t, "adm")
	k, _ := e.ks.Create(keys.Spec{Name: "k"}, "")
	body := `{"key_id":"` + k.ID + `","models":["glm-5.2"]}`
	pv := e.do(t, "POST", "/api/agents/workbuddy/preview", body, reqOpt{})
	if pv.Code != 200 || strings.Contains(pv.Body.String(), k.Secret) {
		t.Fatalf("预览不应包含密钥明文：%d %s", pv.Code, pv.Body.String())
	}
	if !strings.Contains(pv.Body.String(), agentcfg.MaskSecret(k.Secret)) {
		t.Fatalf("预览应含遮掩后的密钥：%s", pv.Body.String())
	}
	rd := e.do(t, "POST", "/api/agents/workbuddy/render", body, reqOpt{remote: true})
	if rd.Code != 200 || !strings.Contains(rd.Body.String(), k.Secret) {
		t.Fatalf("渲染应返回完整片段（远程部署时复制用）：%d", rd.Code)
	}
	if _, err := os.Stat(e.wbFile()); err == nil {
		t.Fatal("预览和渲染都不应写文件")
	}
}

func TestAgentDefaultModelMustBeSelected(t *testing.T) {
	e := newAgentAPI(t, "adm")
	k, _ := e.ks.Create(keys.Spec{Name: "k"}, "")
	rec := e.do(t, "POST", "/api/agents/claude-code/apply", `{"key_id":"`+k.ID+`","models":["glm-5.2"],"default_model":"hy3"}`, reqOpt{})
	if rec.Code != 400 {
		t.Fatalf("默认模型不在已选范围应拒绝：%d", rec.Code)
	}
	rec = e.do(t, "POST", "/api/agents/claude-code/apply", `{"key_id":"`+k.ID+`","models":["glm-5.2"]}`, reqOpt{})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "主模型") {
		t.Fatalf("Claude Code 缺少主模型应拒绝：%d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "POST", "/api/agents/claude-code/apply", `{"key_id":"`+k.ID+`","models":["glm-5.2"],"default_model":"glm-5.2"}`, reqOpt{})
	if rec.Code != 200 {
		t.Fatalf("code=%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(readTestFile(t, filepath.Join(e.home, ".claude", "settings.json")), `"ANTHROPIC_MODEL": "glm-5.2"`) {
		t.Fatal("应写入主模型")
	}
}

func TestAgentRemoveRestores(t *testing.T) {
	e := newAgentAPI(t, "adm")
	k, _ := e.ks.Create(keys.Spec{Name: "k"}, "")
	orig := "{\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"https://old\"\n  }\n}\n"
	file := filepath.Join(e.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	apply := `{"key_id":"` + k.ID + `","models":["glm-5.2"],"default_model":"glm-5.2"}`
	if rec := e.do(t, "POST", "/api/agents/claude-code/apply", apply, reqOpt{}); rec.Code != 200 {
		t.Fatalf("apply %d %s", rec.Code, rec.Body.String())
	}
	list := decode[struct{ Agents []agentcfg.Info }](t, e.do(t, "GET", "/api/agents", "", reqOpt{}))
	for _, a := range list.Agents {
		if a.ID == "claude-code" && !a.Imported {
			t.Fatal("写入后应显示已导入")
		}
	}
	if rec := e.do(t, "POST", "/api/agents/claude-code/remove", `{}`, reqOpt{remote: true}); rec.Code != http.StatusForbidden {
		t.Fatalf("非本机不能移除：%d", rec.Code)
	}
	rec := e.do(t, "POST", "/api/agents/claude-code/remove", `{}`, reqOpt{})
	if rec.Code != 200 {
		t.Fatalf("remove %d %s", rec.Code, rec.Body.String())
	}
	if got := readTestFile(t, file); got != orig {
		t.Fatalf("移除后应还原\n想要：%q\n实际：%q", orig, got)
	}
	if rec := e.do(t, "POST", "/api/agents/claude-code/remove", `{}`, reqOpt{}); rec.Code != 400 {
		t.Fatalf("没有可移除的内容应提示：%d", rec.Code)
	}
}

func TestAgentDetectWithProfile(t *testing.T) {
	e := newAgentAPI(t, "adm")
	for _, p := range []string{"desktop", "web"} {
		dir := filepath.Join(e.home, ".dsh", "profiles", p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cordis.patch.yml"), []byte("[]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	info := decode[agentcfg.Info](t, e.do(t, "POST", "/api/agents/deepseek-harness/detect", `{"profile":"web"}`, reqOpt{}))
	if info.Profile != "web" || len(info.Profiles) != 2 || !strings.Contains(info.Files[0].Path, "web") {
		t.Fatalf("应按所选档案返回：%+v", info)
	}
	bad := decode[agentcfg.Info](t, e.do(t, "POST", "/api/agents/deepseek-harness/detect", `{"profile":"../x"}`, reqOpt{}))
	if bad.Blocked == "" {
		t.Fatal("非法档案名应给出 Blocked")
	}
}

func TestResolveBase(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Host = "localhost:7863"
	if got := requestBase(r); got != "http://127.0.0.1:7863" {
		t.Fatalf("本机 localhost 应换成 127.0.0.1：%s", got)
	}
	r.RemoteAddr = "10.0.0.5:1"
	r.Host = "pool.lan:7863"
	if got := requestBase(r); got != "http://pool.lan:7863" {
		t.Fatalf("远程请求保留主机名：%s", got)
	}
	if got, err := resolveBase(r, "https://gw.example.com/pool/"); err != nil || got != "https://gw.example.com/pool" {
		t.Fatalf("自定义地址：%s %v", got, err)
	}
	for _, bad := range []string{"ftp://x", "gw.example.com", "http://"} {
		if _, err := resolveBase(r, bad); err == nil {
			t.Fatalf("%q 应当被拒绝", bad)
		}
	}
}

func readTestFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取 %s：%v", p, err)
	}
	return string(b)
}
