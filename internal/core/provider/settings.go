package provider

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ---- 平台参数（面板「设置 → 平台」通用渲染） ----

// SettingKind 参数类型，决定面板控件与值的校验方式。
type SettingKind string

const (
	KindBool     SettingKind = "bool"
	KindInt      SettingKind = "int"
	KindFloat    SettingKind = "float"
	KindDuration SettingKind = "duration" // "30m" / "2h"
	KindHours    SettingKind = "hours"    // 一天中的整点列表 [9, 21]
	KindSelect   SettingKind = "select"
	KindText     SettingKind = "text"
	KindSecret   SettingKind = "secret" // 读取时不回显
)

// SettingOption 下拉选项。
type SettingOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SettingField 一个平台参数。Key 是该平台配置段内的 JSON 路径（点分），
// 如 "pool.max_in_flight" 对应 providers.<id>.pool.max_in_flight。
type SettingField struct {
	Key     string          `json:"key"`
	Label   string          `json:"label"`
	Hint    string          `json:"hint,omitempty"`
	Group   string          `json:"group"`
	Kind    SettingKind     `json:"kind"`
	Options []SettingOption `json:"options,omitempty"`
	Min     *float64        `json:"min,omitempty"`
	Max     *float64        `json:"max,omitempty"`
	Unit    string          `json:"unit,omitempty"`
	// Restart 修改后需重启服务才生效（否则即时生效）。
	Restart bool `json:"restart,omitempty"`
	// EnabledBy 仅当该 bool 字段为 true 时可编辑（面板置灰），如时点依赖开关。
	EnabledBy string `json:"enabled_by,omitempty"`
}

// Range 构造 Min/Max。
func Range(min, max float64) (*float64, *float64) { return &min, &max }

// SettingsCapable 可在面板修改参数的平台。
type SettingsCapable interface {
	// SettingsSchema 参数声明（顺序即面板展示顺序）。
	SettingsSchema() []SettingField
	// SettingsRaw 当前生效的完整配置段（含默认值），面板据此按 Key 取值。
	SettingsRaw() json.RawMessage
	// ApplySettings 校验整段新配置；通过后热应用可即时生效的字段。
	// 校验失败时不得修改任何运行状态。返回仍需重启才生效的 Key。
	ApplySettings(raw json.RawMessage) (pendingRestart []string, err error)
	// PendingRestart 自启动以来已修改、但需重启才生效的 Key。
	PendingRestart() []string
}

// SettingsValues 按 schema 从配置段中取值（secret 不回显，只报告是否已设置）。
func SettingsValues(raw json.RawMessage, fields []SettingField) (values map[string]any, secretsSet []string) {
	var root map[string]any
	_ = json.Unmarshal(raw, &root)
	values = map[string]any{}
	for _, f := range fields {
		v, _ := getPath(root, f.Key)
		if f.Kind == KindSecret {
			if s, _ := v.(string); s != "" {
				secretsSet = append(secretsSet, f.Key)
			}
			values[f.Key] = ""
			continue
		}
		values[f.Key] = v
	}
	return values, secretsSet
}

// MergeSettings 把面板提交的值（按 schema 校验类型）写入配置段，返回新配置段。
// 未在 schema 中声明的 Key 一律拒绝，防止面板改到不该改的字段。
func MergeSettings(raw json.RawMessage, fields []SettingField, values map[string]any) (json.RawMessage, error) {
	root := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, err
		}
	}
	byKey := make(map[string]SettingField, len(fields))
	for _, f := range fields {
		byKey[f.Key] = f
	}
	for k, v := range values {
		f, ok := byKey[k]
		if !ok {
			return nil, fmt.Errorf("未知参数 %s", k)
		}
		cv, err := coerce(f, v)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", f.Label, err)
		}
		setPath(root, k, cv)
	}
	return json.Marshal(root)
}

func coerce(f SettingField, v any) (any, error) {
	num := func() (float64, error) {
		switch x := v.(type) {
		case float64:
			return x, nil
		case string:
			return strconv.ParseFloat(strings.TrimSpace(x), 64)
		}
		return 0, fmt.Errorf("需要数字")
	}
	inRange := func(n float64) error {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("需要数字")
		}
		if f.Min != nil && n < *f.Min {
			return fmt.Errorf("不能小于 %v", *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return fmt.Errorf("不能大于 %v", *f.Max)
		}
		return nil
	}
	switch f.Kind {
	case KindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("需要开关值")
		}
		return b, nil
	case KindInt:
		n, err := num()
		if err != nil {
			return nil, err
		}
		if n != math.Trunc(n) {
			return nil, fmt.Errorf("需要整数")
		}
		return int64(n), inRange(n)
	case KindFloat:
		n, err := num()
		if err != nil {
			return nil, err
		}
		return n, inRange(n)
	case KindDuration:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("需要时长，如 30m、2h")
		}
		s = strings.TrimSpace(s)
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("%q 不是合法时长（如 90s、30m、2h）", s)
		}
		if err := inRange(d.Seconds()); err != nil {
			return nil, err
		}
		return s, nil
	case KindHours:
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("需要整点列表")
		}
		seen := map[int]bool{}
		out := []int{}
		for _, x := range arr {
			n, ok := x.(float64)
			if !ok || n != math.Trunc(n) || n < 0 || n > 23 {
				return nil, fmt.Errorf("整点只能是 0-23")
			}
			if !seen[int(n)] {
				seen[int(n)] = true
				out = append(out, int(n))
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("至少选一个整点")
		}
		sortInts(out)
		return out, nil
	case KindSelect:
		s, _ := v.(string)
		for _, o := range f.Options {
			if o.Value == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%q 不是可选值", s)
	case KindText, KindSecret:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("需要文本")
		}
		return strings.TrimSpace(s), nil
	}
	return nil, fmt.Errorf("不支持的类型 %s", f.Kind)
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func getPath(root map[string]any, key string) (any, bool) {
	var cur any = root
	for _, p := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(root map[string]any, key string, v any) {
	parts := strings.Split(key, ".")
	m := root
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

// DiffKeys 比较两个配置段在给定 Key 上的取值，返回不同的 Key（用于计算待重启项）。
func DiffKeys(a, b json.RawMessage, keys []string) []string {
	var ma, mb map[string]any
	_ = json.Unmarshal(a, &ma)
	_ = json.Unmarshal(b, &mb)
	var out []string
	for _, k := range keys {
		va, _ := getPath(ma, k)
		vb, _ := getPath(mb, k)
		ja, _ := json.Marshal(va)
		jb, _ := json.Marshal(vb)
		if string(ja) != string(jb) {
			out = append(out, k)
		}
	}
	return out
}
