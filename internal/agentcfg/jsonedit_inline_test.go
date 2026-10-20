package agentcfg

import "testing"

func TestJSONEditInlineRoundTrip(t *testing.T) {
	cases := map[string]struct {
		src  string
		path []string
	}{
		"inline-array": {"{\n  \"a\": [\"x\", \"y\"]\n}\n", []string{"a"}},
		"one-element":  {"{\n  \"a\": [\"x\"]\n}\n", []string{"a"}},
		"empty-array":  {"{\n  \"a\": []\n}\n", []string{"a"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := mustEditor(t, c.src)
			if err := e.ArrayAppend("new", c.path...); err != nil {
				t.Fatal(err)
			}
			n := e.Len(c.path...)
			if err := e.ArrayDelete(n-1, c.path...); err != nil {
				t.Fatal(err)
			}
			if got := string(e.Bytes()); got != c.src {
				t.Fatalf("追加再删除应还原\n想要：%q\n实际：%q", c.src, got)
			}
		})
	}
	e := mustEditor(t, `{"a": 1, "b": 2}`)
	if err := e.Set(3, "c"); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete("c"); err != nil {
		t.Fatal(err)
	}
	if got := string(e.Bytes()); got != `{"a": 1, "b": 2}` {
		t.Fatalf("单行对象增删后应还原：%q", got)
	}
	for _, key := range []string{"a", "b"} {
		e := mustEditor(t, `{"a": 1, "b": 2}`)
		if err := e.Delete(key); err != nil {
			t.Fatal(err)
		}
		if ok, _ := e.Get(new(int), key); ok {
			t.Fatalf("%s 应已删除：%q", key, e.Bytes())
		}
	}
}
