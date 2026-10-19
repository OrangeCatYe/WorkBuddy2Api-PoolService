package workbuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
)

// offlineTransport 把上游一律应答为 401（非 5xx：不会触发计费类调用的 2s/4s 退避重试）。
// 导入账号时会执行首登动作（签到、查余额、国际版激活），测试里全部落到这个桩上，
// 不会向真实上游发送任何请求。
type offlineTransport struct{}

func (offlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"code":401,"msg":"offline test stub"}`)),
		Request:    r,
	}, nil
}

// newExportProvider 建一个不 Start、上游全部离线的 Provider，并向池里加入几个账号。
func newExportProvider(t *testing.T, docs ...string) *Provider {
	t.Helper()
	s, err := ParseSettings(json.RawMessage(`{"schedule":{"balance_refresh_enabled":false}}`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	p.up.HTTP = &http.Client{Transport: offlineTransport{}}
	p.up.ChatHTTP = &http.Client{Transport: offlineTransport{}}
	// 不 Start（Start 会拉起调度器并可能带着测试凭证访问上游）。注意不能调 p.Stop()：
	// 用量记录器的 Stop 会等待一个只有 Start 才会启动的协程，未 Start 时会永久阻塞。
	// 这里只收掉构造时已启动的两个后台协程。
	t.Cleanup(func() { p.reqlog.Close(); p.pool.Close() })
	for _, d := range docs {
		a, err := auth.Parse([]byte(d))
		if err != nil {
			t.Fatal(err)
		}
		p.pool.Add(a)
	}
	return p
}

const (
	cnDoc = `{"auth":{"accessToken":"at-1","refreshToken":"rt-1","expiresAt":2000,"domain":"copilot.tencent.com","realm":"cn"},` +
		`"account":{"uid":"u-1","enterpriseId":"ent-9","nickname":"甲"},"device_token":"dev-1"}`
	globalDoc = `{"auth":{"accessToken":"at-2","refreshToken":"rt-2","expiresAt":3000,"domain":"www.workbuddy.ai","realm":"global"},` +
		`"account":{"uid":"u-2","nickname":"乙"}}`
	thirdDoc = `{"auth":{"accessToken":"at-3","refreshToken":"rt-3","expiresAt":4000},"account":{"uid":"u-3","nickname":"丙"}}`
)

func exportedItems(t *testing.T, res *provider.AccountExport) []json.RawMessage {
	t.Helper()
	var items []json.RawMessage
	if err := json.Unmarshal(res.Data, &items); err != nil {
		t.Fatalf("导出内容必须是 JSON 数组: %v\n%s", err, res.Data)
	}
	return items
}

// 导出的每一项都能被导入侧的解析原样还原，字段不丢（导入 = 添加账号 → 导入凭证）。
func TestExportRoundTripsThroughImportParser(t *testing.T) {
	p := newExportProvider(t, cnDoc, globalDoc)
	res, err := p.ExportAccounts(context.Background(), []string{"u-1", "u-2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 2 || len(res.Missing) != 0 {
		t.Fatalf("count=%d missing=%v", res.Count, res.Missing)
	}
	items := exportedItems(t, res)
	if len(items) != 2 {
		t.Fatalf("items=%d", len(items))
	}

	a1, err := parseImported(items[0])
	if err != nil {
		t.Fatal(err)
	}
	if a1.UID != "u-1" || a1.AccessToken != "at-1" || a1.RefreshToken != "rt-1" || a1.ExpiresAt != 2000 ||
		a1.Domain != "copilot.tencent.com" || a1.EnterpriseID != "ent-9" || a1.Nickname != "甲" || a1.DeviceToken != "dev-1" ||
		a1.RealmStored() != "cn" {
		t.Fatalf("cn 凭证回环不一致: %+v", a1)
	}
	a2, err := parseImported(items[1])
	if err != nil {
		t.Fatal(err)
	}
	if a2.UID != "u-2" || a2.AccessToken != "at-2" || a2.Realm() != "global" || a2.RealmStored() != "global" {
		t.Fatalf("global 凭证回环不一致: %+v realm=%s", a2, a2.Realm())
	}
}

// 导出的单项形态与 auths/*.json 落盘一致（嵌套形），且不泄漏本地文件路径。
func TestExportMatchesOnDiskShape(t *testing.T) {
	p := newExportProvider(t, cnDoc)
	res, err := p.ExportAccounts(context.Background(), []string{"u-1"})
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(res.Data, &items); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"auth", "account", "device_token"} {
		if _, ok := items[0][k]; !ok {
			t.Errorf("缺少顶层键 %q", k)
		}
	}
	if s := string(res.Data); strings.Contains(strings.ToLower(s), "filepath") || strings.Contains(s, ".json") {
		t.Errorf("导出内容不应包含本地路径: %s", s)
	}
}

// 只导出被选中的账号，保持请求顺序；重复、空白、不存在的 ID 不影响其他账号。
func TestExportOnlySelectedInRequestOrder(t *testing.T) {
	p := newExportProvider(t, cnDoc, globalDoc, thirdDoc)
	res, err := p.ExportAccounts(context.Background(), []string{"u-3", "ghost", "u-1", "u-3", " ", "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 2 {
		t.Fatalf("count=%d, want 2", res.Count)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "ghost" {
		t.Fatalf("missing=%v", res.Missing)
	}
	items := exportedItems(t, res)
	first, _ := parseImported(items[0])
	second, _ := parseImported(items[1])
	if first.UID != "u-3" || second.UID != "u-1" {
		t.Fatalf("顺序应与请求一致: %s, %s", first.UID, second.UID)
	}
	if strings.Contains(string(res.Data), "at-2") {
		t.Fatal("未选中的账号不得出现在导出内容里")
	}
}

func TestExportNothingExportableIsError(t *testing.T) {
	p := newExportProvider(t, cnDoc)
	if _, err := p.ExportAccounts(context.Background(), []string{"ghost"}); err == nil {
		t.Fatal("没有任何可导出账号时必须报错")
	}
	if _, err := p.ExportAccounts(context.Background(), nil); err == nil {
		t.Fatal("空选择必须报错")
	}
}

func TestProviderAdvertisesExportCapability(t *testing.T) {
	p := newExportProvider(t)
	var _ provider.AccountExporter = p
	for _, c := range provider.MetaWithCaps(p).Capabilities {
		if c == provider.CapExport {
			return
		}
	}
	t.Fatal("WorkBuddy 应声明 export 能力")
}

// 导入时「保留较新凭证」的判定边界。
func TestShouldKeepExisting(t *testing.T) {
	mk := func(exp int64) *auth.Auth { return &auth.Auth{UID: "u", AccessToken: "x", ExpiresAt: exp} }
	cases := []struct {
		name     string
		cur, in  *auth.Auth
		wantKeep bool
	}{
		{"池里没有该账号", nil, mk(100), false},
		{"池里的更新", mk(200), mk(100), true},
		{"过期时间相同（同一份令牌）", mk(100), mk(100), true},
		{"导入内容更新（如重新授权）", mk(100), mk(200), false},
		{"导入内容没有过期时间，按原行为覆盖", mk(500), mk(0), false},
	}
	for _, c := range cases {
		if got := shouldKeepExisting(c.cur, c.in); got != c.wantKeep {
			t.Errorf("%s: got %v, want %v", c.name, got, c.wantKeep)
		}
	}
}

// 整批导入内容都不比池里的新时：全部跳过、不覆盖现有凭证、不算失败，且不触发任何网络请求。
func TestImportSkipsOlderThanExisting(t *testing.T) {
	p := newExportProvider(t, cnDoc) // u-1 expiresAt=2000
	older := `[{"auth":{"accessToken":"STALE","refreshToken":"STALE-RT","expiresAt":1000},"account":{"uid":"u-1","nickname":"旧"}}]`
	st, err := p.importJSON(context.Background(), []byte(older))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "success" || !strings.Contains(st.Message, "导入 0 / 1") || !strings.Contains(st.Message, "跳过 1") {
		t.Fatalf("status=%+v", st)
	}
	if got := p.pool.AuthByUID("u-1").AccessTokenValue(); got != "at-1" {
		t.Fatalf("较旧的导入内容不得覆盖现有凭证，当前 accessToken=%q", got)
	}
}

// credSnap 凭证快照（加锁读取，避免与后台刷新竞争）。
type credSnap struct {
	at, rt, domain, uid, ent, nick, dev, realm string
	exp                                        int64
}

func snap(a *auth.Auth) credSnap {
	realm := a.RealmStored() // 自带锁，须在 a.Lock 之前取
	a.Lock()
	defer a.Unlock()
	return credSnap{a.AccessToken, a.RefreshToken, a.Domain, a.UID, a.EnterpriseID, a.Nickname, a.DeviceToken, realm, a.ExpiresAt}
}

// 完整回环：A 实例导出勾选的账号 → 作为文件内容交给 B 实例的「导入凭证」→ B 里得到
// 完全相同的凭证，并且已落盘、重启后可被重新加载。
func TestExportThenImportRestoresAccountsAcrossInstances(t *testing.T) {
	ctx := context.Background()
	src := newExportProvider(t, cnDoc, globalDoc, thirdDoc)
	dst := newExportProvider(t)

	res, err := src.ExportAccounts(ctx, []string{"u-1", "u-2"}) // 故意不导出 u-3
	if err != nil {
		t.Fatal(err)
	}
	st, err := dst.importJSON(ctx, res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "success" || !strings.Contains(st.Message, "导入 2 / 2") {
		t.Fatalf("status=%+v", st)
	}

	for _, uid := range []string{"u-1", "u-2"} {
		got, want := dst.pool.AuthByUID(uid), src.pool.AuthByUID(uid)
		if got == nil {
			t.Fatalf("%s 导入后应在池中", uid)
		}
		if snap(got) != snap(want) {
			t.Fatalf("%s 导入后凭证与导出前不一致:\n got=%+v\nwant=%+v", uid, snap(got), snap(want))
		}
	}
	if dst.pool.AuthByUID("u-3") != nil {
		t.Fatal("未勾选的账号不应出现在目标实例")
	}

	// 已落盘：重新扫描凭证目录能得到同样的账号与凭证。
	onDisk, err := auth.LoadDir(dst.settings().AuthDir)
	if err != nil || len(onDisk) != 2 {
		t.Fatalf("auth 目录应有 2 个凭证文件: n=%d err=%v", len(onDisk), err)
	}
	for _, a := range onDisk {
		if snap(a) != snap(src.pool.AuthByUID(a.UID)) {
			t.Fatalf("落盘凭证与源实例不一致: %s", a.UID)
		}
	}
}

// 把同一份文件再导入一次：不会覆盖、不会失败，结果里说明跳过；
// 而内容更新（如重新授权后导出）则照常覆盖。
func TestReimportIsIdempotentAndNewerWins(t *testing.T) {
	ctx := context.Background()
	src := newExportProvider(t, cnDoc)
	dst := newExportProvider(t)
	res, err := src.ExportAccounts(ctx, []string{"u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := dst.importJSON(ctx, res.Data); err != nil || st.State != "success" || !strings.Contains(st.Message, "导入 1 / 1") {
		t.Fatalf("首次导入: %+v %v", st, err)
	}

	st, err := dst.importJSON(ctx, res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "success" || !strings.Contains(st.Message, "导入 0 / 1") || !strings.Contains(st.Message, "跳过 1") {
		t.Fatalf("重复导入应整体跳过: %+v", st)
	}

	newer := strings.NewReplacer(`"at-1"`, `"at-1-renewed"`, `"expiresAt": 2000`, `"expiresAt": 9000`).Replace(string(res.Data))
	if newer == string(res.Data) {
		t.Fatal("测试数据未被替换，检查导出格式")
	}
	if st, err := dst.importJSON(ctx, []byte(newer)); err != nil || !strings.Contains(st.Message, "导入 1 / 1") {
		t.Fatalf("更新的凭证应覆盖: %+v %v", st, err)
	}
	if got := snap(dst.pool.AuthByUID("u-1")); got.at != "at-1-renewed" || got.exp != 9000 {
		t.Fatalf("覆盖后凭证不对: %+v", got)
	}
}
