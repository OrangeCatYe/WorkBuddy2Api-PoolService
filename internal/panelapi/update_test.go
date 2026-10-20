package panelapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/update"
)

type updEnv struct {
	api *API
	cfg *config.Config
	mux *http.ServeMux
}

// newUpdEnv 装配一个带更新服务的面板 API。配置修改会真实落盘到临时目录，便于检查是否写对。
func newUpdEnv(t *testing.T, adminKey string) *updEnv {
	t.Helper()
	cfg := config.Default()
	cfg.AdminKey = adminKey
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cfg = withPath(t, cfg)
	e := &updEnv{cfg: cfg}
	updater := func(mut func(*config.Config)) error {
		next := e.cfg.Clone()
		mut(next)
		if err := next.Save(); err != nil {
			return err
		}
		e.cfg = next
		return nil
	}
	e.api = New(func() *config.Config { return e.cfg }, updater, provider.NewRegistry(), nil)
	e.api.SetUpdater(update.New(func() *config.Config { return e.cfg }, "v1.0.0"))
	e.mux = http.NewServeMux()
	e.api.Register(e.mux)
	return e
}

// withPath 让配置能落盘（Config.path 未导出，借助 Load 创建）。
func withPath(t *testing.T, c *config.Config) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	loaded, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.AdminKey, loaded.DataDir = c.AdminKey, c.DataDir
	return loaded
}

func (e *updEnv) do(method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func TestUpdateRoutesRequireAuth(t *testing.T) {
	e := newUpdEnv(t, "adm-secret")
	for _, c := range [][2]string{
		{"GET", "/api/update/status"}, {"POST", "/api/update/check"}, {"POST", "/api/update/download"},
		{"POST", "/api/update/apply"}, {"GET", "/api/settings/update"}, {"PUT", "/api/settings/update"},
	} {
		if rec := e.do(c[0], c[1], "", `{}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 无密钥应 401，实际 %d", c[0], c[1], rec.Code)
		}
		if rec := e.do(c[0], c[1], "wrong", `{}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 错误密钥应 401，实际 %d", c[0], c[1], rec.Code)
		}
	}
}

func TestUpdateStatusShape(t *testing.T) {
	e := newUpdEnv(t, "adm-secret")
	rec := e.do("GET", "/api/update/status", "adm-secret", "")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("code=%d cc=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var st update.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.State != update.StateIdle || st.Current.Version != "v1.0.0" || !st.Current.Release {
		t.Errorf("%+v", st)
	}
}

// 空 admin_key 意味着面板无鉴权：能下载并替换程序的接口必须拒绝，只读与检查不受影响。
func TestStateChangingUpdateEndpointsRefuseWithoutAdminKey(t *testing.T) {
	e := newUpdEnv(t, "")
	for _, c := range [][3]string{
		{"POST", "/api/update/download", `{}`},
		{"POST", "/api/update/apply", `{}`},
		{"PUT", "/api/settings/update", `{"source":"github"}`},
	} {
		rec := e.do(c[0], c[1], "", c[2])
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "admin_key") {
			t.Errorf("%s %s 应 403 并说明原因: %d %s", c[0], c[1], rec.Code, rec.Body.String())
		}
	}
	if rec := e.do("GET", "/api/update/status", "", ""); rec.Code != 200 {
		t.Errorf("状态只读，应可访问: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/settings/update", "", ""); rec.Code != 200 {
		t.Errorf("读取设置应可访问: %d", rec.Code)
	}
}

func TestUpdateActionsMapErrorsToStatusCodes(t *testing.T) {
	e := newUpdEnv(t, "k")
	if rec := e.do("POST", "/api/update/check", "k", `{"source":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("未知下载源应 400: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/update/check", "k", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("坏请求体应 400: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/update/download", "k", ``); rec.Code != http.StatusBadRequest {
		t.Errorf("未检查就下载应 400: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/update/apply", "k", ``); rec.Code != http.StatusBadRequest {
		t.Errorf("未下载就安装应 400: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateRoutesWithoutUpdaterReturn501(t *testing.T) {
	e := newUpdEnv(t, "k")
	e.api.SetUpdater(nil)
	if rec := e.do("GET", "/api/update/status", "k", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("未装配更新服务应 501: %d", rec.Code)
	}
}

// ---- 设置 ----

func TestUpdateSettingsRoundTrip(t *testing.T) {
	e := newUpdEnv(t, "k")
	rec := e.do("GET", "/api/settings/update", "k", "")
	var s struct {
		Source        string          `json:"source"`
		Sources       []update.Source `json:"sources"`
		AutoCheck     bool            `json:"auto_check"`
		CheckInterval string          `json:"check_interval"`
	}
	json.Unmarshal(rec.Body.Bytes(), &s)
	if s.Source != "github" || len(s.Sources) != 1 || !s.Sources[0].Builtin || s.AutoCheck || s.CheckInterval != "24h0m0s" {
		t.Fatalf("默认设置: %s", rec.Body.String())
	}

	body := `{"source":"gitee","proxy":"http://127.0.0.1:7890","auto_check":true,"check_interval":"6h","sources":[
		{"id":"github","name":"被忽略的内置同名条目","builtin":true,"manifest_url":"https://evil.example.com/x","asset_url":"https://evil.example.com/{name}"},
		{"id":"gitee","name":"Gitee 镜像","manifest_url":"https://gitee.com/x/y/raw/latest.json","asset_url":"https://gitee.com/x/y/releases/{tag}/{name}"}]}`
	rec = e.do("PUT", "/api/settings/update", "k", body)
	if rec.Code != 200 {
		t.Fatalf("保存失败: %d %s", rec.Code, rec.Body.String())
	}
	u := e.cfg.Update
	if u.Source != "gitee" || !u.AutoCheck || u.CheckInterval.Hours() != 6 || u.Proxy != "http://127.0.0.1:7890" {
		t.Errorf("配置未按预期保存: %+v", u)
	}
	if len(u.Sources) != 1 || u.Sources[0].ID != "gitee" {
		t.Errorf("只保存自定义源，内置同名条目应被忽略: %+v", u.Sources)
	}
	if update.Builtin().ManifestURL != update.Sources(u)[0].ManifestURL {
		t.Error("内置源地址不应被请求改写")
	}
	// 落盘：重新加载应得到相同配置。
	reloaded, _, err := config.Load(e.cfg.Path())
	if err != nil || reloaded.Update.Source != "gitee" || reloaded.Update.CheckInterval.Hours() != 6 {
		t.Errorf("配置应已写入文件: %+v %v", reloaded.Update, err)
	}
}

func TestUpdateSettingsValidation(t *testing.T) {
	e := newUpdEnv(t, "k")
	good := `{"id":"m","name":"镜像","manifest_url":"https://m.example.com/latest.json","asset_url":"https://m.example.com/{name}"}`
	cases := map[string]string{
		"选中不存在的源":    `{"source":"ghost","sources":[]}`,
		"清单是明文 http": `{"source":"github","sources":[{"id":"m","name":"m","manifest_url":"http://m.example.com/latest.json","asset_url":"https://m.example.com/{name}"}]}`,
		"安装包缺 name":  `{"source":"github","sources":[{"id":"m","name":"m","manifest_url":"https://m.example.com/latest.json","asset_url":"https://m.example.com/pkg.zip"}]}`,
		"源 ID 重复":    `{"source":"github","sources":[` + good + `,` + good + `]}`,
		"代理格式错误":     `{"source":"github","proxy":"127.0.0.1:7890"}`,
		"代理协议不支持":    `{"source":"github","proxy":"ftp://h:1"}`,
		"检查间隔太短":     `{"source":"github","check_interval":"10m"}`,
		"检查间隔无法解析":   `{"source":"github","check_interval":"soon"}`,
		"坏请求体":       `not json`,
	}
	for name, body := range cases {
		if rec := e.do("PUT", "/api/settings/update", "k", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s：应 400，实际 %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if e.cfg.Update.Source != "github" || len(e.cfg.Update.Sources) != 0 {
		t.Errorf("校验失败不得修改配置: %+v", e.cfg.Update)
	}
}

// 代理地址里的密码：读取时隐藏；前端原样回传（没改）时必须保留真实密码，不能把 *** 存进去。
func TestProxyPasswordMaskedAndPreserved(t *testing.T) {
	e := newUpdEnv(t, "k")
	secret := "http://alice:s3cret@127.0.0.1:7890"
	if rec := e.do("PUT", "/api/settings/update", "k", `{"source":"github","proxy":"`+secret+`"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.cfg.Update.Proxy != secret {
		t.Fatalf("应保存原始地址: %q", e.cfg.Update.Proxy)
	}
	rec := e.do("GET", "/api/settings/update", "k", "")
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("接口不得返回代理密码: %s", rec.Body.String())
	}
	var got struct {
		Proxy string `json:"proxy"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !strings.Contains(got.Proxy, "alice") || !strings.Contains(got.Proxy, "***") {
		t.Errorf("应返回隐藏密码的地址: %q", got.Proxy)
	}
	// 前端把读到的值原样存回：真实密码必须保留。
	if rec := e.do("PUT", "/api/settings/update", "k", `{"source":"github","auto_check":true,"proxy":"`+got.Proxy+`"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.cfg.Update.Proxy != secret || !e.cfg.Update.AutoCheck {
		t.Errorf("未修改代理时应保留原密码: %q", e.cfg.Update.Proxy)
	}
	// 清空代理。
	e.do("PUT", "/api/settings/update", "k", `{"source":"github","proxy":""}`)
	if e.cfg.Update.Proxy != "" {
		t.Errorf("应能清空代理: %q", e.cfg.Update.Proxy)
	}
}

// 用真实签名的清单走一遍 HTTP 层，确认检查接口把状态原样带给前端。
func TestUpdateCheckEndpointEndToEnd(t *testing.T) {
	e := newUpdEnv(t, "k")
	// 自定义源指向一个不存在的本机端口：网络失败应体现在 status.error 里，而不是 HTTP 错误。
	body := `{"source":"local","sources":[{"id":"local","name":"本机","manifest_url":"http://127.0.0.1:1/latest.json","asset_url":"http://127.0.0.1:1/{name}"}]}`
	if rec := e.do("PUT", "/api/settings/update", "k", body); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec := e.do("POST", "/api/update/check", "k", `{}`)
	if rec.Code != 200 {
		t.Fatalf("网络失败应作为状态返回而不是 HTTP 错误: %d %s", rec.Code, rec.Body.String())
	}
	var st update.Status
	json.Unmarshal(rec.Body.Bytes(), &st)
	if st.State != update.StateError || !strings.Contains(st.Error, "无法连接") || st.Source != "local" {
		t.Errorf("%+v", st)
	}
}
