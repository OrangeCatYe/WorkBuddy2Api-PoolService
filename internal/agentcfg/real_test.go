package agentcfg

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRealConfigCopies 用本机真实配置的副本验证「写入 → 移除」不会改动其余内容。
//
// 只在设置了 POOLSVC_REAL_HOME（指向真实用户主目录）时运行；所有读写都发生在临时目录的副本上，
// 原文件只被读取。副本里可能含密钥，临时目录会在测试结束后删除。
func TestRealConfigCopies(t *testing.T) {
	real := os.Getenv("POOLSVC_REAL_HOME")
	if real == "" {
		t.Skip("未设置 POOLSVC_REAL_HOME，跳过真实配置副本检查")
	}
	cp := func(rel, home string) bool {
		src := filepath.Join(real, rel)
		in, err := os.Open(src)
		if err != nil {
			return false
		}
		defer in.Close()
		dst := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(dst)
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		return true
	}
	in := testInput()
	in.Default = "glm-5.2"

	t.Run("claude-code", func(t *testing.T) {
		env := testEnv(t)
		if !cp(filepath.Join(".claude", "settings.json"), env.Home) {
			t.Skip("本机没有 Claude Code 配置")
		}
		f := filepath.Join(env.Home, ".claude", "settings.json")
		orig := readFile(t, f)
		h := newHarness(t, claudeCode{}, env)
		h.build(in)
		jsonMap(t, readFile(t, f))
		h.remove(Options{})
		if got := readFile(t, f); got != orig {
			t.Fatalf("移除后与原文件不一致\n想要：%q\n实际：%q", orig, got)
		}
	})

	t.Run("codex", func(t *testing.T) {
		env := testEnv(t)
		if !cp(filepath.Join(".codex", "config.toml"), env.Home) {
			t.Skip("本机没有 Codex 配置")
		}
		f := filepath.Join(env.Home, ".codex", "config.toml")
		orig := readFile(t, f)
		h := newHarness(t, codex{}, env)
		h.build(in)
		if _, err := parseTOML(readFile(t, f)); err != nil {
			t.Fatal(err)
		}
		h.remove(Options{})
		if got := readFile(t, f); got != orig {
			t.Fatalf("移除后与原文件不一致\n想要：%q\n实际：%q", orig, got)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		env := testEnv(t)
		got1 := cp(filepath.Join(".config", "opencode", "opencode.json"), env.Home)
		cp(filepath.Join(".config", "opencode", "opencode.jsonc"), env.Home)
		if !got1 {
			t.Skip("本机没有 OpenCode 配置")
		}
		f := filepath.Join(env.Home, ".config", "opencode", "opencode.json")
		orig := readFile(t, f)
		h := newHarness(t, openCode{}, env)
		h.build(in)
		jsonMap(t, readFile(t, f))
		h.remove(Options{})
		if got := readFile(t, f); got != orig {
			t.Fatalf("移除后与原文件不一致\n想要：%q\n实际：%q", orig, got)
		}
	})

	t.Run("deepseek-harness", func(t *testing.T) {
		env := testEnv(t)
		ents, err := os.ReadDir(filepath.Join(real, ".dsh", "profiles"))
		if err != nil {
			t.Skip("本机没有 DSH")
		}
		for _, e := range ents {
			if e.IsDir() {
				cp(filepath.Join(".dsh", "profiles", e.Name(), dshPatchFile), env.Home)
				cp(filepath.Join(".dsh", "profiles", e.Name(), "package.json"), env.Home)
			}
		}
		cp(filepath.Join(".dsh", ".credentials.yaml"), env.Home)
		h := newHarness(t, dshAgent{}, env)
		info := h.detect(Options{})
		t.Logf("DSH 默认档案=%s 可选=%v", info.Profile, info.Profiles)
		patch := filepath.Join(env.Home, ".dsh", "profiles", info.Profile, dshPatchFile)
		origPatch := readFile(t, patch)
		credPath := filepath.Join(env.Home, ".dsh", ".credentials.yaml")
		origCred, _ := os.ReadFile(credPath)

		h.build(in)
		if _, err := loadYAML([]byte(readFile(t, patch)), yaml.SequenceNode); err != nil {
			t.Fatalf("写入后无法解析：%v", err)
		}
		h.remove(Options{})

		var a, b any
		if err := yaml.Unmarshal([]byte(origPatch), &a); err != nil {
			t.Skipf("原补丁含 yaml.v3 无法解到 any 的写法，改为节点级比较：%v", err)
		}
		if err := yaml.Unmarshal([]byte(readFile(t, patch)), &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("移除后与原补丁语义不一致")
		}
		if len(origCred) > 0 {
			var ca, cb any
			_ = yaml.Unmarshal(origCred, &ca)
			_ = yaml.Unmarshal([]byte(readFile(t, credPath)), &cb)
			if !reflect.DeepEqual(ca, cb) {
				t.Fatal("移除后与原凭据文件语义不一致")
			}
		}
	})
}
