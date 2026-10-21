package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/prompt"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/upstream"
)

// 渠道校验（11128）的端到端测试。假上游按真实规则拒绝：只看 system 消息的开头，
// 开头不是中性开头行就回 400 + 11128（msg: unapproved channel），与账号、请求头都无关。

const (
	neutralLine       = "You are a helpful assistant."
	channelRejectBody = `{"code":11128,"msg":"Illegal API invocation from an unapproved channel","displayTips":"这次调用没通过渠道校验，可能不是官方客户端发出的"}`
	// 不在任何已知清单里的开头：证明重试不依赖清单是否过时。
	unknownOpening = "You are Some Weird New Agent v9. Do things carefully."
	dshOpening     = "You are an AI agent powered by DeepSeek Harness. You help with coding tasks."
)

// firstSystem 取出站请求体里第一条 system 消息的文本。
func firstSystem(t *testing.T, raw string) string {
	t.Helper()
	var b struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("出站体不是 JSON: %v\n%s", err, raw)
	}
	for _, m := range b.Messages {
		if m.Role == "system" {
			s, _ := m.Content.(string)
			return s
		}
	}
	return ""
}

// channelUpstream 造一个按渠道校验规则拒绝的假上游，并记录每次出站体。
// accept 决定「什么样的 system 开头能通过」；nil 表示以中性开头行开头即可通过。
func channelUpstream(t *testing.T, bodies *[]string, accept func(sys string) bool) *upstream.Client {
	t.Helper()
	if accept == nil {
		accept = func(sys string) bool { return strings.HasPrefix(sys, neutralLine) }
	}
	up := &upstream.Client{
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(r.Body)
			*bodies = append(*bodies, string(raw))
			if !accept(firstSystem(t, string(raw))) {
				return &http.Response{
					StatusCode: 400,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(channelRejectBody)),
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(sseOK)),
			}, nil
		})},
		ChatBaseCN: "https://fake.example",
	}
	up.SanitizeFingerprints.Store(true) // 与生产一致：开头处理受脱敏开关约束
	return up
}

func chat(h *Handler, system, user string, stream bool) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{
		"model":  "glm-5.2",
		"stream": stream,
		"messages": []any{
			map[string]any{"role": "system", "content": system},
			map[string]any{"role": "user", "content": user},
		},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(body))))
	return rec
}

type blockedErr struct {
	Error struct {
		Message     string `json:"message"`
		Code        string `json:"code"`
		GatewayHint string `json:"gateway_hint"`
	} `json:"error"`
}

func decodeBlocked(t *testing.T, rec *httptest.ResponseRecorder) blockedErr {
	t.Helper()
	var e blockedErr
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("响应不是错误信封: %v\n%s", err, rec.Body.String())
	}
	return e
}

func newChannelHandler(t *testing.T, up *upstream.Client, cfg Config) (*Handler, *auth.Auth) {
	t.Helper()
	a := &auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999}
	p := testPoolWith(a)
	cfg.Pool, cfg.Upstream = p, up
	return NewHandler(cfg), a
}

// 清单外的新指纹：补一行中性开头行重试即可通过，不降级（宿主提示原文保留）；
// 通过之后学下来，同样开头的下一个请求出站前就已处理，不再先撞一次 400。
func TestChannelRejectFixedByNeutralOpeningWithoutDegrade(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, nil)
	h, _ := newChannelHandler(t, up, Config{PromptMode: "passthrough"})

	if rec := chat(h, unknownOpening, "hi", true); rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if len(bodies) != 2 {
		t.Fatalf("应为 1 次被拒 + 1 次补开头行重试，实际 %d 次", len(bodies))
	}
	if got := firstSystem(t, bodies[0]); got != unknownOpening {
		t.Fatalf("第一次应原样发出：%q", got)
	}
	if got := firstSystem(t, bodies[1]); got != neutralLine+"\n"+unknownOpening {
		t.Fatalf("重试应只在最前面补一行，宿主提示原文一字不动：%q", got)
	}
	if h.degrade.Active() {
		t.Fatal("补开头行就通过了，不应触发全局降级（降级会让所有客户端丢掉自己的系统提示词）")
	}
	if up.LearnedChannelOpenings() != 1 {
		t.Fatalf("通过之后应学到这个开头：%d", up.LearnedChannelOpenings())
	}

	// 下一个同样开头的请求：只发 1 次，且出站时就已补好开头行。
	if rec := chat(h, unknownOpening+" Plus more tail.", "again", true); rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if len(bodies) != 3 {
		t.Fatalf("学习之后同样开头的请求应只发 1 次，累计应为 3 次，实际 %d", len(bodies))
	}
	if !strings.HasPrefix(firstSystem(t, bodies[2]), neutralLine+"\n"+unknownOpening) {
		t.Fatalf("第二个请求出站前就应补好开头行：%q", firstSystem(t, bodies[2]))
	}
}

// 非流式响应走另一条成功分支，同样要学习。
func TestChannelRejectFixLearnsOnNonStreamSuccess(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, nil)
	h, _ := newChannelHandler(t, up, Config{PromptMode: "passthrough"})
	if rec := chat(h, unknownOpening, "hi", false); rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if len(bodies) != 2 || up.LearnedChannelOpenings() != 1 {
		t.Fatalf("非流式也应补开头行重试并学习：calls=%d learned=%d", len(bodies), up.LearnedChannelOpenings())
	}
}

// 已知指纹（DeepSeek Harness）：出站前就补好开头行，上游一次都不会看到原样的指纹，只发 1 次。
func TestChannelRejectKnownOpeningNeverSentRaw(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, nil)
	h, _ := newChannelHandler(t, up, Config{PromptMode: "passthrough"})
	if rec := chat(h, dshOpening, "hi", true); rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if len(bodies) != 1 {
		t.Fatalf("已知指纹应在出站前处理，只发 1 次，实际 %d", len(bodies))
	}
	if got := firstSystem(t, bodies[0]); got != neutralLine+"\n"+dshOpening {
		t.Fatalf("应补一行中性开头、原文不动：%q", got)
	}
	if h.degrade.Active() || up.LearnedChannelOpenings() != 0 {
		t.Fatal("主动处理不应触发降级，也没有可学习的东西")
	}
}

// 补开头行没用、是降级提示词救回来的：不能把开头记成「已修复」。
func TestChannelRejectDoesNotLearnWhenOnlyDegradeHelped(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, func(sys string) bool { return strings.HasPrefix(sys, prompt.Degraded) })
	h, _ := newChannelHandler(t, up, Config{PromptMode: "passthrough"})
	if rec := chat(h, unknownOpening, "hi", true); rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if len(bodies) != 3 {
		t.Fatalf("应为 原样 → 补开头行 → 降级，共 3 次，实际 %d", len(bodies))
	}
	if !h.degrade.Active() {
		t.Fatal("补开头行也没用，应走到降级")
	}
	if up.LearnedChannelOpenings() != 0 {
		t.Fatal("通过的是降级提示词而不是补开头行，不能把开头记成已修复")
	}
}

// 无论怎么改写都被拒：回 content_blocked，原文透传，hint 回显被拒的开头并说明已重试过，
// 不再把矛头指向 system 开头；账号不受罚。
func TestChannelRejectUnfixableEchoesEvidence(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, func(string) bool { return false })
	h, a := newChannelHandler(t, up, Config{PromptMode: "passthrough"})
	rec := chat(h, unknownOpening, "hi", true)
	if rec.Code != 400 {
		t.Fatalf("code=%d want 400 content_blocked body=%s", rec.Code, rec.Body)
	}
	if len(bodies) != 3 {
		t.Fatalf("应为 原样 → 补开头行 → 降级，共 3 次，实际 %d", len(bodies))
	}
	e := decodeBlocked(t, rec)
	if e.Error.Code != "content_blocked" {
		t.Fatalf("code=%q", e.Error.Code)
	}
	if !strings.Contains(e.Error.Message, "unapproved channel") {
		t.Fatalf("message 应原样透传上游原文：%s", e.Error.Message)
	}
	for _, want := range []string{"channel check", unknownOpening, "already retried", "switching accounts will not help"} {
		if !strings.Contains(e.Error.GatewayHint, want) {
			t.Fatalf("hint 缺少 %q：%s", want, e.Error.GatewayHint)
		}
	}
	if strings.Contains(strings.ToLower(e.Error.GatewayHint), "upstream") {
		t.Fatalf("hint 不应含 upstream 字样：%s", e.Error.GatewayHint)
	}
	if st, _ := h.cfg.Pool.Status(a.UID); st.Cooling || st.Disabled || st.ErrTotal != 0 {
		t.Fatalf("渠道校验拦截不应罚账号: %+v", st)
	}
}

// 回显的证据取的是「第一次被拒时实际上线的开头」，不是被降级改写之后的。
func TestChannelRejectEvidenceIsTheFirstAttemptsOpening(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, func(string) bool { return false })
	h, _ := newChannelHandler(t, up, Config{PromptMode: "passthrough"})
	e := decodeBlocked(t, chat(h, "\n\n"+unknownOpening+"\nsecond line is not echoed", "hi", true))
	if !strings.Contains(e.Error.GatewayHint, `"`+unknownOpening+`"`) || strings.Contains(e.Error.GatewayHint, "second line") {
		t.Fatalf("应回显第一行非空内容：%s", e.Error.GatewayHint)
	}
}

// custom 模式 system 是网关自己的提示词：不做补开头行重试、不降级，只发 1 次；
// hint 不再建议切到 custom。
func TestChannelRejectCustomModeSendsOnceAndAdvisesCorrectly(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, func(string) bool { return false })
	h, _ := newChannelHandler(t, up, Config{PromptMode: "custom", PromptText: "SYS"})
	rec := chat(h, unknownOpening, "hi", true)
	if rec.Code != 400 || len(bodies) != 1 {
		t.Fatalf("custom 模式应只发 1 次并回 400：code=%d calls=%d", rec.Code, len(bodies))
	}
	hint := decodeBlocked(t, rec).Error.GatewayHint
	if !strings.Contains(hint, "gateway's own") || strings.Contains(hint, "already retried") || strings.Contains(hint, "set providers.workbuddy.prompt.mode") {
		t.Fatalf("custom 模式的建议不对：%s", hint)
	}
	if h.degrade.Active() {
		t.Fatal("custom 模式不应降级")
	}
}

// 补开头行这一步不占轮转名额：MaxRotate 很小时，后面的降级重试不能被饿死，
// 末端也不能把这次被拒误报成「无可用账号」。
func TestChannelRejectDoesNotStarveRotationSlots(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, func(string) bool { return false })
	h, _ := newChannelHandler(t, up, Config{PromptMode: "passthrough", MaxRotate: 2})
	rec := chat(h, unknownOpening, "hi", true)
	if e := decodeBlocked(t, rec); rec.Code != 400 || e.Error.Code != "content_blocked" {
		t.Fatalf("MaxRotate=2 时仍应回 content_blocked，而不是 no_healthy_account：code=%d %s", rec.Code, rec.Body)
	}
	if len(bodies) != 3 {
		t.Fatalf("应为 原样 → 补开头行 → 降级，共 3 次，实际 %d", len(bodies))
	}
}

// 这一步只发生一次：不会因为反复被拒而死循环。
func TestChannelRejectRetryIsBounded(t *testing.T) {
	var bodies []string
	up := channelUpstream(t, &bodies, func(string) bool { return false })
	h, _ := newChannelHandler(t, up, Config{PromptMode: "append", PromptText: "GATEWAY PROMPT", MaxRotate: 10})
	chat(h, unknownOpening, "hi", true)
	if len(bodies) > 3 {
		t.Fatalf("补开头行与降级各最多一次，共不超过 3 次，实际 %d", len(bodies))
	}
}
