package upstream

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	dshOpening = "You are an AI agent powered by DeepSeek Harness. You help with coding tasks."
	ccSentence = "You are Claude Code, Anthropic's official CLI for Claude."
)

func chanDecode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, b)
	}
	return m
}

func msgContent(t *testing.T, body []byte, i int) any {
	t.Helper()
	msgs := chanDecode(t, body)["messages"].([]any)
	return msgs[i].(map[string]any)["content"]
}

func sysBody(content string) []byte {
	b, _ := json.Marshal(map[string]any{
		"model": "glm-5.2",
		"messages": []any{
			map[string]any{"role": "system", "content": content},
			map[string]any{"role": "user", "content": "hi"},
		},
	})
	return b
}

// 已知开头：补一行中性开头，宿主提示原文一字不动；大小写、前导空白不影响命中。
func TestPrepareBodyNeutralizesKnownChannelOpening(t *testing.T) {
	for _, in := range []string{dshOpening, "  \n" + strings.ToUpper(dshOpening), "you are an ai agent powered by deepseek harness"} {
		out := PrepareBodyOpt(sysBody(in), true)
		got, _ := msgContent(t, out, 0).(string)
		if got != neutralOpening+"\n"+in {
			t.Fatalf("应只在最前面补一行，原文不动\n想要：%q\n实际：%q", neutralOpening+"\n"+in, got)
		}
		if u, _ := msgContent(t, out, 1).(string); u != "hi" {
			t.Fatalf("user 消息被改动：%q", u)
		}
	}
}

// 未命中的请求：system 原文逐字不动（不为了「保险」给所有请求都加一行）。
func TestPrepareBodyLeavesUnrelatedSystemUntouched(t *testing.T) {
	for _, in := range []string{
		"You are Kimi, a helpful assistant.",
		"你是一名工程助手。",
		// 同一句放在提示中段：网关只看开头，不处理。
		"Some preface.\n" + dshOpening,
	} {
		out := PrepareBodyOpt(sysBody(in), true)
		if got, _ := msgContent(t, out, 0).(string); got != in {
			t.Fatalf("不应改动 %q，实际 %q", in, got)
		}
	}
}

// 幂等：已经以中性开头行开头的不重复补；重复经过管线结果稳定。
func TestPrepareBodyNeutralizeIsIdempotent(t *testing.T) {
	once := PrepareBodyOpt(sysBody(dshOpening), true)
	twice := PrepareBodyOpt(once, true)
	if string(once) != string(twice) {
		t.Fatalf("重复处理应当幂等\n%s\n%s", once, twice)
	}
}

// 每一条 system 消息都会被网关扫描：第二条命中同样要处理（developer 先被归一为 system）。
func TestPrepareBodyNeutralizesEverySystemMessageIncludingDeveloper(t *testing.T) {
	src, _ := json.Marshal(map[string]any{"model": "m", "messages": []any{
		map[string]any{"role": "system", "content": "fine, normal prompt"},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "developer", "content": dshOpening},
	}})
	out := PrepareBodyOpt(src, true)
	if got, _ := msgContent(t, out, 0).(string); got != "fine, normal prompt" {
		t.Fatalf("第一条不应被改动：%q", got)
	}
	if got, _ := msgContent(t, out, 2).(string); !strings.HasPrefix(got, neutralOpening+"\n") {
		t.Fatalf("第二条 system（由 developer 归一而来）应补开头行：%q", got)
	}
}

// 多模态数组形态：在最前面插一个 text part，其余 part 不动。
func TestPrepareBodyNeutralizesArrayContent(t *testing.T) {
	src, _ := json.Marshal(map[string]any{"model": "m", "messages": []any{
		map[string]any{"role": "system", "content": []any{
			map[string]any{"type": "text", "text": dshOpening},
			map[string]any{"type": "text", "text": "second"},
		}},
	}})
	out := PrepareBodyOpt(src, true)
	parts, _ := msgContent(t, out, 0).([]any)
	if len(parts) != 3 {
		t.Fatalf("应多出一个 part：%v", parts)
	}
	if first := parts[0].(map[string]any)["text"]; first != neutralOpening {
		t.Fatalf("第一个 part 应为中性开头：%v", first)
	}
	if parts[1].(map[string]any)["text"] != dshOpening || parts[2].(map[string]any)["text"] != "second" {
		t.Fatalf("原有 part 被改动：%v", parts)
	}
}

// 受脱敏开关约束：关闭即完全还原（与 TestChatStreamWireBodySanitizeDisabled 同口径）。
func TestPrepareBodyChannelOpeningHonorsSanitizeSwitch(t *testing.T) {
	out := PrepareBodyOpt(sysBody(dshOpening), false)
	if got, _ := msgContent(t, out, 0).(string); got != dshOpening {
		t.Fatalf("关闭脱敏后不应补开头行：%q", got)
	}
}

// Claude Code 身份句：脱敏开启时已被改写成 "CLI tool for Claude"，不再命中清单，
// 与引入本功能之前逐字节一致（不额外补一行）；关闭脱敏时原样保留。
func TestPrepareBodyClaudeCodeSentenceKeepsPreviousBehavior(t *testing.T) {
	out := PrepareBodyOpt(sysBody(ccSentence), true)
	got, _ := msgContent(t, out, 0).(string)
	if got != "You are Claude Code, Anthropic's official CLI tool for Claude." {
		t.Fatalf("脱敏后应只改一个词、不补开头行：%q", got)
	}
}

// 强制模式（撞上拦截之后的重试）：不依赖清单，所有 system / developer 消息都补；
// 已经以中性开头开头的、非 system 的不动；返回的键供学习。
func TestNeutralizeSystemOpeningsForce(t *testing.T) {
	src, _ := json.Marshal(map[string]any{"model": "m", "stream": true, "messages": []any{
		map[string]any{"role": "system", "content": "A brand new agent prompt that is not on any list"},
		map[string]any{"role": "developer", "content": neutralOpening + " already"},
		map[string]any{"role": "user", "content": "Another brand new thing"},
	}})
	out, keys := NeutralizeSystemOpenings(src)
	if len(keys) != 1 || keys[0] != "a brand new agent prompt that is not on any list" {
		t.Fatalf("应只改动第一条并返回其键：%v", keys)
	}
	if got, _ := msgContent(t, out, 0).(string); got != neutralOpening+"\nA brand new agent prompt that is not on any list" {
		t.Fatalf("第一条：%q", got)
	}
	if got, _ := msgContent(t, out, 1).(string); got != neutralOpening+" already" {
		t.Fatalf("已以中性开头开头的不应改动：%q", got)
	}
	if got, _ := msgContent(t, out, 2).(string); got != "Another brand new thing" {
		t.Fatalf("user 消息不应改动：%q", got)
	}
	if chanDecode(t, out)["stream"] != true {
		t.Fatal("其余字段应原样保留")
	}
	// 没有可改动的：原样返回。
	same, none := NeutralizeSystemOpenings(sysBody(neutralOpening + " ok"))
	if none != nil || string(same) != string(sysBody(neutralOpening+" ok")) {
		t.Fatal("没有可改动的消息时应原样返回")
	}
	// 无法解析 / 空 body 不 panic、原样返回。
	if b, k := NeutralizeSystemOpenings([]byte("not json")); string(b) != "not json" || k != nil {
		t.Fatal("坏 JSON 应原样返回")
	}
	if b, k := NeutralizeSystemOpenings(nil); len(b) != 0 || k != nil {
		t.Fatal("空 body 应原样返回")
	}
}

// 学习：补开头后通过的开头记进 Client，之后出站前就处理；太短的不学；有上限；中性开头本身不学。
func TestClientLearnsChannelOpenings(t *testing.T) {
	c := New()
	const unknown = "You are Brand New Agent 9000, built by somebody."
	before := c.prepareBody(sysBody(unknown), "cn", "u1", "")
	if got, _ := msgContent(t, before, 0).(string); got != unknown {
		t.Fatalf("未学习前清单外的开头不应被处理：%q", got)
	}
	_, keys := NeutralizeSystemOpenings(sysBody(unknown))
	c.LearnChannelOpenings(keys)
	if c.LearnedChannelOpenings() != 1 {
		t.Fatalf("应学到 1 条：%d", c.LearnedChannelOpenings())
	}
	after := c.prepareBody(sysBody(unknown+" And a different tail."), "cn", "u1", "")
	if got, _ := msgContent(t, after, 0).(string); !strings.HasPrefix(got, neutralOpening+"\n") {
		t.Fatalf("学习之后同样开头的请求出站前就应处理：%q", got)
	}
	// 另一个 Client 不受影响（按实例持有）。
	other := New().prepareBody(sysBody(unknown), "cn", "u1", "")
	if got, _ := msgContent(t, other, 0).(string); got != unknown {
		t.Fatalf("学习集合不应跨 Client：%q", got)
	}

	c.LearnChannelOpenings([]string{"hi", "", strings.ToLower(neutralOpening)})
	if c.LearnedChannelOpenings() != 1 {
		t.Fatalf("太短 / 空 / 中性开头本身都不应被学习：%d", c.LearnedChannelOpenings())
	}
	c.LearnChannelOpenings(keys) // 重复不增长
	if c.LearnedChannelOpenings() != 1 {
		t.Fatal("重复学习不应增长")
	}
	for i := 0; i < maxLearnedOpenings+10; i++ {
		c.LearnChannelOpenings([]string{"opening number " + strings.Repeat("x", 5) + string(rune('a'+i%26)) + strings.Repeat("y", i)})
	}
	if c.LearnedChannelOpenings() > maxLearnedOpenings {
		t.Fatalf("学习集合应有上限：%d", c.LearnedChannelOpenings())
	}
}

func TestSystemOpeningsEvidence(t *testing.T) {
	long := strings.Repeat("长", evidenceMaxRunes+30)
	src, _ := json.Marshal(map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "\n\n  first real line  \nsecond line"},
		map[string]any{"role": "user", "content": "ignored"},
		map[string]any{"role": "developer", "content": []any{map[string]any{"type": "text", "text": "array opening"}}},
		map[string]any{"role": "system", "content": long},
		map[string]any{"role": "system", "content": "fourth is dropped"},
	}})
	got := SystemOpenings(src)
	if len(got) != evidenceMaxLines {
		t.Fatalf("最多取 %d 条：%v", evidenceMaxLines, got)
	}
	if got[0] != "first real line" || got[1] != "array opening" {
		t.Fatalf("取第一行非空内容、数组取第一个文本 part：%v", got)
	}
	if r := []rune(got[2]); len(r) != evidenceMaxRunes+1 || !strings.HasSuffix(got[2], "…") {
		t.Fatalf("超长应截断并加省略号：%d %q", len(r), got[2])
	}
	for _, bad := range [][]byte{nil, []byte("nope"), []byte(`{"messages":"x"}`), []byte(`{"messages":[]}`)} {
		if SystemOpenings(bad) != nil {
			t.Fatalf("读不出来应返回空，不编造：%s", bad)
		}
	}
}

// 回显的是「真实上线的字节」：命中已知指纹时是补上的那行中性开头，而不是宿主以为自己发了什么。
func TestWireSystemOpeningsReportsWhatActuallyGoesOut(t *testing.T) {
	c := New()
	got := c.WireSystemOpenings("cn", sysBody(dshOpening))
	if len(got) != 1 || got[0] != neutralOpening {
		t.Fatalf("应回显上线的开头：%v", got)
	}
}

// 判据只认 msg 文案，不认 displayTips：同码另外两道门提示语一字不差，不能当判据。
func TestIsChannelRejectOnlyTrustsMsg(t *testing.T) {
	yes := []string{
		`{"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`,
		`{"code":11128,"msg":"ILLEGAL API INVOCATION"}`,
	}
	no := []string{
		`{"code":11128,"msg":"conversation id missing","displayTips":"这次调用没通过渠道校验，可能不是官方客户端发出的"}`,
		`{"code":11128,"msg":"first message is not system prompt"}`,
		`{"code":11140,"msg":"request illegal"}`,
		``,
	}
	for _, b := range yes {
		if !IsChannelReject(b) {
			t.Errorf("应判为渠道校验：%s", b)
		}
	}
	for _, b := range no {
		if IsChannelReject(b) {
			t.Errorf("不应判为渠道校验：%s", b)
		}
	}
}

func TestContentBlockedHint(t *testing.T) {
	channel := `{"code":11128,"msg":"Illegal API invocation from an unapproved channel","displayTips":"可能不是官方客户端发出的"}`
	// 非渠道校验（审核类拦截）：保持原文案。
	if got := GatewayHint(ErrContentBlocked, `{"code":11128,"msg":"blocked by security policy"}`, HintContext{}); got != "request content was rejected by content policy; adjust the prompt and retry" {
		t.Fatalf("非渠道校验应保持原文案：%q", got)
	}
	got := GatewayHint(ErrContentBlocked, channel, HintContext{SystemOpenings: []string{"You are Foo", "bar"}})
	for _, want := range []string{"channel check", "unrelated to the account", `"You are Foo", "bar"`, "reword the opening", "prompt.mode"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q：%s", want, got)
		}
	}
	retried := GatewayHint(ErrContentBlocked, channel, HintContext{SystemOpenings: []string{"x"}, PromptRetried: true})
	if !strings.Contains(retried, "already retried") || strings.Contains(retried, "reword the opening") {
		t.Fatalf("已重试仍被拒时不应再把矛头指向 system 开头：%s", retried)
	}
	custom := GatewayHint(ErrContentBlocked, channel, HintContext{ModeCustom: true})
	if strings.Contains(custom, "set providers.workbuddy.prompt.mode") || !strings.Contains(custom, "gateway's own") {
		t.Fatalf("custom 模式不应再建议切到 custom：%s", custom)
	}
	// content_blocked 响应不含上游字样的既有口径。
	for _, h := range []string{got, retried, custom} {
		if strings.Contains(strings.ToLower(h), "upstream") {
			t.Fatalf("hint 不应含 upstream 字样：%s", h)
		}
	}
}
