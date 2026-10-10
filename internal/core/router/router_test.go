package router

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/orangeruan/poolservice/internal/core/account"
	"github.com/orangeruan/poolservice/internal/core/provider"
)

func TestMapEffort(t *testing.T) {
	five := []string{"low", "medium", "high", "xhigh", "max"}
	three := []string{"low", "medium", "high"}
	cases := []struct {
		req       string
		supported []string
		want      string
		mapped    bool
	}{
		{"", three, "", false},
		{"auto", three, "", false},
		{"high", three, "high", false},
		{"HIGH", three, "high", false},
		{"xhigh", three, "high", true},
		{"max", three, "high", true},
		{"max", five, "max", false},
		{"xhigh", five, "xhigh", false},
		{"minimal", three, "low", true},
		{"low", []string{"high", "max"}, "high", true},
		{"none", []string{"none", "low", "high"}, "none", false},
		{"none", three, "low", true},
		{"low", []string{"none", "high"}, "high", true},
		{"max", nil, "max", false},
		{"weird", three, "weird", false},
		{"maximum", five, "max", false},
	}
	for _, c := range cases {
		got, mapped := MapEffort(c.req, c.supported)
		if got != c.want || mapped != c.mapped {
			t.Errorf("MapEffort(%q,%v)=(%q,%v) want (%q,%v)", c.req, c.supported, got, mapped, c.want, c.mapped)
		}
	}
}

type fakeProvider struct {
	id     string
	models []provider.Model
	err    error
}

func (f *fakeProvider) Meta() provider.Meta                              { return provider.Meta{ID: f.id, Name: f.id} }
func (f *fakeProvider) Accounts() []account.Account                      { return nil }
func (f *fakeProvider) Models(context.Context) ([]provider.Model, error) { return f.models, f.err }
func (f *fakeProvider) ServeChat(context.Context, *provider.ChatRequest, http.ResponseWriter) provider.ChatOutcome {
	return provider.ChatOutcome{}
}

func TestCatalogResolve(t *testing.T) {
	reg := provider.NewRegistry()
	a := &fakeProvider{id: "workbuddy", models: []provider.Model{
		{ID: "glm-5.2", Aliases: []string{"cn:glm-5.2"}},
		{ID: "deepseek-v4"},
	}}
	b := &fakeProvider{id: "other", models: []provider.Model{{ID: "glm-5.2"}, {ID: "kimi-k3"}}}
	_ = reg.Register(a)
	_ = reg.Register(b)
	c := NewCatalog(reg, 0)
	ctx := context.Background()

	check := func(name, wantProv, wantModel string) {
		t.Helper()
		e, err := c.Resolve(ctx, name)
		if err != nil {
			t.Fatalf("Resolve(%q) err=%v", name, err)
		}
		if e.Provider != wantProv || e.Model.ID != wantModel {
			t.Fatalf("Resolve(%q)=%s/%s want %s/%s", name, e.Provider, e.Model.ID, wantProv, wantModel)
		}
	}
	check("glm-5.2", "workbuddy", "glm-5.2") // 同名取优先级高者
	check("other/glm-5.2", "other", "glm-5.2")
	check("CN:GLM-5.2", "workbuddy", "glm-5.2")
	check("kimi-k3", "other", "kimi-k3")
	if _, err := c.Resolve(ctx, "nope"); !errors.As(err, &ErrUnknownModel{}) {
		t.Fatalf("want ErrUnknownModel, got %v", err)
	}

	// Provider 失败时保留上次结果
	b.err = errors.New("down")
	c.Refresh(ctx)
	check("kimi-k3", "other", "kimi-k3")
	if c.Errors()["other"] == "" {
		t.Fatal("expected provider error recorded")
	}
}
