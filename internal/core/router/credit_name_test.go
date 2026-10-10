package router

import (
	"context"
	"testing"

	"github.com/orangeruan/poolservice/internal/core/provider"
)

func TestStripCreditSuffix(t *testing.T) {
	cases := map[string]string{
		"glm-5.2(x0.05)":         "glm-5.2",
		"glm-5.2 (x0.05)":        "glm-5.2",
		"glm-5.2[x1]":            "glm-5.2",
		"glm-5.2（x0.5）":          "glm-5.2",
		"glm-5.2(0.50x)":         "glm-5.2",
		"glm-5.2(x0.29 credits)": "glm-5.2",
		"glm-5.2(免费)":            "glm-5.2",
		"glm-5.2":                "glm-5.2",
		"model(preview)":         "model(preview)",
		"global:gpt-5.4(x2)":     "global:gpt-5.4",
	}
	for in, want := range cases {
		if got := StripCreditSuffix(in); got != want {
			t.Errorf("StripCreditSuffix(%q)=%q want %q", in, got, want)
		}
	}
	if got := DecorateCredit("glm-5.2", "x0.05"); got != "glm-5.2(x0.05)" {
		t.Errorf("DecorateCredit=%q", got)
	}
}

// 倍率随优惠变化后，客户端保存的旧名仍能解析。
func TestResolveDecoratedName(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(&fakeProvider{id: "workbuddy", models: []provider.Model{{ID: "glm-5.2", CreditRateText: "x0.1"}}})
	c := NewCatalog(reg, 0)
	for _, name := range []string{"glm-5.2(x0.1)", "glm-5.2(x0.05)", "workbuddy/glm-5.2(x0.1)"} {
		e, err := c.Resolve(context.Background(), name)
		if err != nil || e.Model.ID != "glm-5.2" {
			t.Errorf("Resolve(%q) = %v, %v", name, e.Model.ID, err)
		}
	}
}
