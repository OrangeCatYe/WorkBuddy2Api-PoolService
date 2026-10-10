package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeSettings(t *testing.T) {
	min, max := Range(0, 10)
	fields := []SettingField{
		{Key: "pool.max", Label: "并发", Kind: KindInt, Min: min, Max: max},
		{Key: "pool.rate", Label: "速度", Kind: KindFloat},
		{Key: "on", Label: "开关", Kind: KindBool},
		{Key: "cd", Label: "冷却", Kind: KindDuration},
		{Key: "s.hours", Label: "时间", Kind: KindHours},
		{Key: "mode", Label: "模式", Kind: KindSelect, Options: []SettingOption{{Value: "a"}, {Value: "b"}}},
		{Key: "tok", Label: "密钥", Kind: KindSecret},
	}
	raw := json.RawMessage(`{"keep":1,"pool":{"other":true},"tok":"secret"}`)
	out, err := MergeSettings(raw, fields, map[string]any{
		"pool.max": float64(4), "pool.rate": "0.5", "on": true, "cd": " 30m ",
		"s.hours": []any{float64(21), float64(9), float64(9)}, "mode": "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"cd":"30m","keep":1,"mode":"b","on":true,"pool":{"max":4,"other":true,"rate":0.5},"s":{"hours":[9,21]},"tok":"secret"}`
	if string(out) != want {
		t.Fatalf("merged = %s", out)
	}

	bad := []struct {
		k   string
		v   any
		msg string
	}{
		{"pool.max", float64(11), "不能大于"},
		{"pool.max", 1.5, "整数"},
		{"on", "yes", "开关"},
		{"cd", "soon", "时长"},
		{"s.hours", []any{float64(24)}, "0-23"},
		{"s.hours", []any{}, "至少"},
		{"mode", "c", "可选值"},
		{"unknown", 1, "未知参数"},
	}
	for _, c := range bad {
		if _, err := MergeSettings(raw, fields, map[string]any{c.k: c.v}); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s=%v err=%v", c.k, c.v, err)
		}
	}

	vals, secrets := SettingsValues(out, fields)
	if vals["pool.max"] != float64(4) || vals["tok"] != "" || len(secrets) != 1 || secrets[0] != "tok" {
		t.Fatalf("values = %v secrets = %v", vals, secrets)
	}
	if d := DiffKeys(raw, out, []string{"keep", "mode", "tok"}); len(d) != 1 || d[0] != "mode" {
		t.Fatalf("diff = %v", d)
	}
}
