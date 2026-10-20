package agentcfg

import (
	"strings"
	"testing"
)

func mustEditor(t *testing.T, src string) *jsonEditor {
	t.Helper()
	e, err := newJSONEditor([]byte(src))
	if err != nil {
		t.Fatalf("newJSONEditor: %v", err)
	}
	return e
}

func TestJSONEditSetKeepsCommentsAndFormat(t *testing.T) {
	src := "{\n  // 我的注释\n  \"a\": 1,\n  \"b\": {\n    \"c\": true /* 行内 */\n  }\n}\n"
	e := mustEditor(t, src)
	if err := e.Set(Obj{{"x", 1}, {"y", []string{"p", "q"}}}, "b", "poolservice"); err != nil {
		t.Fatal(err)
	}
	got := string(e.Bytes())
	for _, want := range []string{"// 我的注释", "/* 行内 */", `"a": 1,`, `"poolservice": {`, `"x": 1`} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q：\n%s", want, got)
		}
	}
	var out map[string]any
	ok, err := e.Get(&out, "b")
	if err != nil || !ok {
		t.Fatalf("Get: %v %v", ok, err)
	}
	if out["c"] != true {
		t.Fatalf("原有字段被破坏：%v", out)
	}
}

func TestJSONEditSetThenDeleteRestoresBytes(t *testing.T) {
	cases := map[string]string{
		"pretty":   "{\n  \"a\": 1,\n  \"b\": 2\n}\n",
		"crlf":     "{\r\n  \"a\": 1,\r\n  \"b\": 2\r\n}\r\n",
		"tab":      "{\n\t\"a\": 1\n}\n",
		"comment":  "{\n  \"a\": 1 // 说明\n}\n",
		"trailing": "{\n  \"a\": 1,\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			e := mustEditor(t, src)
			if err := e.Set(Obj{{"k", "v"}}, "new"); err != nil {
				t.Fatal(err)
			}
			if string(e.Bytes()) == src {
				t.Fatal("写入没有生效")
			}
			if err := e.Delete("new"); err != nil {
				t.Fatal(err)
			}
			if got := string(e.Bytes()); got != src {
				t.Fatalf("删除后未还原\n想要：%q\n实际：%q", src, got)
			}
		})
	}
}

func TestJSONEditIdempotent(t *testing.T) {
	e := mustEditor(t, "{\n  \"a\": 1\n}\n")
	v := Obj{{"npm", "@ai-sdk/openai-compatible"}, {"models", Obj{{"m1", Obj{{"name", "M1"}}}}}}
	if err := e.Set(v, "provider", "poolservice"); err != nil {
		t.Fatal(err)
	}
	first := string(e.Bytes())
	if err := e.Set(v, "provider", "poolservice"); err != nil {
		t.Fatal(err)
	}
	if string(e.Bytes()) != first {
		t.Fatalf("重复写入应当幂等\n%s\n---\n%s", first, e.Bytes())
	}
}

func TestJSONEditDeleteMembers(t *testing.T) {
	src := "{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3\n}\n"
	for key, want := range map[string]string{
		"a": "{\n  \"b\": 2,\n  \"c\": 3\n}\n",
		"b": "{\n  \"a\": 1,\n  \"c\": 3\n}\n",
		"c": "{\n  \"a\": 1,\n  \"b\": 2\n}\n",
	} {
		e := mustEditor(t, src)
		if err := e.Delete(key); err != nil {
			t.Fatal(err)
		}
		if got := string(e.Bytes()); got != want {
			t.Fatalf("删除 %s\n想要：%q\n实际：%q", key, want, got)
		}
	}
	e := mustEditor(t, "{\n  \"only\": 1\n}\n")
	if err := e.Delete("only"); err != nil {
		t.Fatal(err)
	}
	if got := string(e.Bytes()); got != "{}\n" {
		t.Fatalf("删除唯一成员：%q", got)
	}
	if err := e.Delete("not-exist", "x"); err != nil {
		t.Fatalf("删除不存在的路径应当无操作：%v", err)
	}
}

func TestJSONEditBOMAndCRLF(t *testing.T) {
	src := utf8BOM + "{\r\n  \"a\": 1\r\n}\r\n"
	e := mustEditor(t, src)
	if err := e.Set(Obj{{"k", Obj{{"n", 1}}}}, "b"); err != nil {
		t.Fatal(err)
	}
	got := string(e.Bytes())
	if !strings.HasPrefix(got, utf8BOM) {
		t.Fatal("BOM 丢失")
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("出现了裸 LF：%q", got)
	}
}

func TestJSONEditCompactDocStaysCompact(t *testing.T) {
	e := mustEditor(t, `{"a":1,"b":{"c":2}}`)
	if err := e.Set(Obj{{"x", 1}, {"y", Obj{{"z", 2}}}}, "b", "d"); err != nil {
		t.Fatal(err)
	}
	got := string(e.Bytes())
	if strings.Contains(got, "\n") {
		t.Fatalf("单行文档不应被改成多行：%q", got)
	}
	if !strings.Contains(got, `"d": {"x":1,"y":{"z":2}}`) {
		t.Fatalf("内容不符：%q", got)
	}
}

func TestJSONEditFreshFile(t *testing.T) {
	e := mustEditor(t, "")
	if err := e.Set("https://opencode.ai/config.json", "$schema"); err != nil {
		t.Fatal(err)
	}
	if err := e.Set(Obj{{"name", "PoolService"}}, "provider", "poolservice"); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"provider\": {\n    \"poolservice\": {\n      \"name\": \"PoolService\"\n    }\n  }\n}\n"
	if got := string(e.Bytes()); got != want {
		t.Fatalf("新建文件格式不符\n想要：%q\n实际：%q", want, got)
	}
}

func TestJSONEditArrays(t *testing.T) {
	e := mustEditor(t, "{\n  \"models\": [\n    {\n      \"id\": \"a\"\n    }\n  ],\n  \"x\": 1\n}\n")
	if err := e.ArrayAppend(Obj{{"id", "b"}, {"vendor", "PoolService"}}, "models"); err != nil {
		t.Fatal(err)
	}
	if e.Len("models") != 2 {
		t.Fatalf("追加失败：%s", e.Bytes())
	}
	var second struct{ ID, Vendor string }
	if err := e.Elem(&second, 1, "models"); err != nil || second.ID != "b" {
		t.Fatalf("读取元素：%v %+v", err, second)
	}
	if err := e.ArraySet(Obj{{"id", "b2"}}, 1, "models"); err != nil {
		t.Fatal(err)
	}
	if err := e.Elem(&second, 1, "models"); err != nil || second.ID != "b2" {
		t.Fatalf("替换失败：%+v", second)
	}
	if err := e.ArrayDelete(1, "models"); err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"models\": [\n    {\n      \"id\": \"a\"\n    }\n  ],\n  \"x\": 1\n}\n"; string(e.Bytes()) != want {
		t.Fatalf("删除后未还原：%q", e.Bytes())
	}
}

func TestJSONEditEmptyArrayAndObject(t *testing.T) {
	e := mustEditor(t, "{\n  \"models\": [],\n  \"p\": {}\n}\n")
	if err := e.ArrayAppend("x", "models"); err != nil {
		t.Fatal(err)
	}
	if err := e.Set(1, "p", "k"); err != nil {
		t.Fatal(err)
	}
	var arr []string
	if ok, err := e.Get(&arr, "models"); !ok || err != nil || len(arr) != 1 || arr[0] != "x" {
		t.Fatalf("%v %v %v", ok, err, arr)
	}
	var m map[string]int
	if ok, err := e.Get(&m, "p"); !ok || err != nil || m["k"] != 1 {
		t.Fatalf("%v %v %v", ok, err, m)
	}
}

// 用户真实的 opencode.json 里对象开头有一个空白行，缩进也不规整。
func TestJSONEditOddWhitespace(t *testing.T) {
	src := "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"provider\": {\n    \n    \"docode\": {\n      \"npm\": \"x\"\n    }\n  }\n}\n"
	e := mustEditor(t, src)
	if err := e.Set(Obj{{"npm", "y"}}, "provider", "poolservice"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(e.Bytes()), "\"docode\": {\n      \"npm\": \"x\"\n    },") {
		t.Fatalf("已有条目被改动：\n%s", e.Bytes())
	}
	if err := e.Delete("provider", "poolservice"); err != nil {
		t.Fatal(err)
	}
	if string(e.Bytes()) != src {
		t.Fatalf("未还原：\n%q", e.Bytes())
	}
}

func TestJSONEditRejectsInvalid(t *testing.T) {
	for name, src := range map[string]string{
		"单引号":     "{'a': 1}",
		"无引号键":    "{a: 1}",
		"缺少冒号":    `{"a" 1}`,
		"多余内容":    `{"a":1} x`,
		"注释没结束":   "{ /* x }",
		"数组根上写成员": `[1,2]`,
	} {
		e, err := newJSONEditor([]byte(src))
		if name == "数组根上写成员" {
			if err != nil {
				t.Fatalf("数组根应当可解析：%v", err)
			}
			if e.Set(1, "k") == nil {
				t.Fatal("数组根按键写入应当报错")
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s 应当解析失败", name)
		}
	}
}

func TestJSONEditNonObjectOnPath(t *testing.T) {
	e := mustEditor(t, `{"a": 1}`)
	if err := e.Set(2, "a", "b"); err == nil {
		t.Fatal("路径中间不是对象时应当报错")
	}
	if string(e.Bytes()) != `{"a": 1}` {
		t.Fatal("报错后文本不应被改动")
	}
}

func TestStandardizeKeepsLength(t *testing.T) {
	src := "{ // c\n \"a\": [1,2,], /* d */ \"b\": \"//x\", }"
	got := standardize(src)
	if len(got) != len(src) {
		t.Fatalf("长度变化：%d != %d", len(got), len(src))
	}
	var v map[string]any
	e := mustEditor(t, src)
	if ok, err := e.Get(&v); !ok || err != nil {
		t.Fatalf("Get: %v %v", ok, err)
	}
	if v["b"] != "//x" {
		t.Fatalf("字符串里的 // 被当成注释：%v", v)
	}
}
