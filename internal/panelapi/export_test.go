package panelapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
)

// plainProvider 只有基础能力，用于验证「平台不支持导出」时的拒绝。
type plainProvider struct{ id string }

func (p plainProvider) Meta() provider.Meta         { return provider.Meta{ID: p.id, Name: p.id} }
func (p plainProvider) Accounts() []account.Account { return nil }

// fakeExporter 实现导出能力的替身：记录调用，"ghost" 视为不存在。
type fakeExporter struct {
	plainProvider
	calls   int
	lastIDs []string
}

func (f *fakeExporter) ExportAccounts(_ context.Context, ids []string) (*provider.AccountExport, error) {
	f.calls++
	f.lastIDs = ids
	var docs, missing []string
	for _, id := range ids {
		if id == "ghost" {
			missing = append(missing, id)
			continue
		}
		docs = append(docs, `{"auth":{"accessToken":"t-`+id+`"},"account":{"uid":"`+id+`"}}`)
	}
	if len(docs) == 0 {
		return nil, errors.New("所选账号都无法导出")
	}
	return &provider.AccountExport{Data: json.RawMessage("[" + strings.Join(docs, ",") + "]"), Count: len(docs), Missing: missing}, nil
}

func newExportAPI(t *testing.T, adminKey string) (*API, *fakeExporter) {
	t.Helper()
	fe := &fakeExporter{plainProvider: plainProvider{id: "ex"}}
	reg := provider.NewRegistry()
	if err := reg.Register(fe); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plainProvider{id: "plain"}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminKey: adminKey}
	return New(func() *config.Config { return cfg }, nil, reg, nil), fe
}

func post(t *testing.T, a *API, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	a.Register(mux)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestExportEndpoint(t *testing.T) {
	a, fe := newExportAPI(t, "adm-secret")

	t.Run("缺少管理密钥", func(t *testing.T) {
		if rec := post(t, a, "/api/providers/ex/export", "", `{"account_ids":["a"]}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code=%d", rec.Code)
		}
	})
	t.Run("未知平台", func(t *testing.T) {
		if rec := post(t, a, "/api/providers/nope/export", "adm-secret", `{"account_ids":["a"]}`); rec.Code != http.StatusNotFound {
			t.Fatalf("code=%d", rec.Code)
		}
	})
	t.Run("平台不支持导出", func(t *testing.T) {
		if rec := post(t, a, "/api/providers/plain/export", "adm-secret", `{"account_ids":["a"]}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("code=%d", rec.Code)
		}
	})
	t.Run("未选择账号", func(t *testing.T) {
		for _, body := range []string{``, `{}`, `{"account_ids":[]}`, `not json`} {
			if rec := post(t, a, "/api/providers/ex/export", "adm-secret", body); rec.Code != http.StatusBadRequest {
				t.Fatalf("body=%q code=%d", body, rec.Code)
			}
		}
		if fe.calls != 0 {
			t.Fatalf("空选择不应调用平台导出，calls=%d", fe.calls)
		}
	})
	t.Run("平台报错透传为 400", func(t *testing.T) {
		rec := post(t, a, "/api/providers/ex/export", "adm-secret", `{"account_ids":["ghost"]}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "无法导出") {
			t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("正常导出", func(t *testing.T) {
		rec := post(t, a, "/api/providers/ex/export", "adm-secret", `{"account_ids":["a","ghost","b"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("含凭证的响应必须 no-store，got %q", cc)
		}
		var out struct {
			Count    int               `json:"count"`
			Missing  []string          `json:"missing"`
			Accounts []json.RawMessage `json:"accounts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Count != 2 || len(out.Accounts) != 2 || len(out.Missing) != 1 || out.Missing[0] != "ghost" {
			t.Fatalf("out=%+v", out)
		}
		if strings.Join(fe.lastIDs, ",") != "a,ghost,b" {
			t.Fatalf("应把勾选的账号 ID 原样交给平台: %v", fe.lastIDs)
		}
	})
}

// admin_key 为空 = 面板对外无鉴权，此时不得放出凭证。
func TestExportRefusedWithoutAdminKey(t *testing.T) {
	a, fe := newExportAPI(t, "")
	rec := post(t, a, "/api/providers/ex/export", "", `{"account_ids":["a"]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if fe.calls != 0 {
		t.Fatal("拒绝导出时不应触达平台")
	}
	if strings.Contains(rec.Body.String(), "accounts") {
		t.Fatalf("拒绝响应里不得带凭证字段: %s", rec.Body.String())
	}
}

func TestExportCapabilityAdvertised(t *testing.T) {
	fe := &fakeExporter{plainProvider: plainProvider{id: "ex"}}
	found := false
	for _, c := range provider.MetaWithCaps(fe).Capabilities {
		if c == provider.CapExport {
			found = true
		}
	}
	if !found {
		t.Fatal("实现 AccountExporter 的平台应自动带上 export 能力")
	}
	for _, c := range provider.MetaWithCaps(plainProvider{id: "p"}).Capabilities {
		if c == provider.CapExport {
			t.Fatal("未实现的平台不应带 export 能力")
		}
	}
}
