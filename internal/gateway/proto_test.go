package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orangeruan/poolservice/internal/config"
	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/core/router"
	"github.com/orangeruan/poolservice/internal/store/keys"
	"github.com/orangeruan/poolservice/internal/store/requests"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return m
}

func TestAnthropicToChat(t *testing.T) {
	in := decode(t, `{
	  "model":"glm-5.2","max_tokens":1024,"stream":true,
	  "system":[{"type":"text","text":"You are helpful."}],
	  "thinking":{"type":"enabled","budget_tokens":10000},
	  "tools":[{"name":"read","description":"read file","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}],
	  "tool_choice":{"type":"any"},
	  "messages":[
	    {"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}}]},
	    {"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"x"},{"type":"text","text":"ok"},{"type":"tool_use","id":"t1","name":"read","input":{"p":"a"}}]},
	    {"role":"user","content":[{"type":"text","text":"and then?"},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"data"}]}]}
	  ]}`)
	out, stream, err := anthropicToChat(in)
	if err != nil || !stream {
		t.Fatal(err)
	}
	msgs := out["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,user" {
		t.Fatalf("roles = %v", roles)
	}
	if parts, ok := msgs[1].(map[string]any)["content"].([]any); !ok || len(parts) != 2 {
		t.Fatalf("image parts lost: %v", msgs[1])
	}
	asst := msgs[2].(map[string]any)
	if asst["reasoning_content"] != "hmm" || asst["content"] != "ok" {
		t.Fatalf("assistant = %v", asst)
	}
	call := asst["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if call["arguments"] != `{"p":"a"}` {
		t.Fatalf("args = %v", call["arguments"])
	}
	if out["reasoning_effort"] != "medium" || out["tool_choice"] != "required" || len(out["tools"].([]any)) != 1 {
		t.Fatalf("out = %v", out)
	}
	if so := out["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Fatal("include_usage missing")
	}
}

// 经中转 / 网关转发后，messages 里常带 system、developer 角色（标准协议不允许，但不能因此整个请求 400）。
func TestAnthropicToChatAcceptsSystemRoleInMessages(t *testing.T) {
	roles := func(out map[string]any) string {
		var rs []string
		for _, m := range out["messages"].([]any) {
			rs = append(rs, m.(map[string]any)["role"].(string))
		}
		return strings.Join(rs, ",")
	}
	sysText := func(out map[string]any) string {
		return out["messages"].([]any)[0].(map[string]any)["content"].(string)
	}

	// 只有 messages 里的 system：提到最前。
	out, _, err := anthropicToChat(decode(t, `{"model":"m","messages":[
	  {"role":"system","content":"be brief"},
	  {"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("messages 里的 system 角色不应报错: %v", err)
	}
	if roles(out) != "system,user" || sysText(out) != "be brief" {
		t.Fatalf("roles=%s sys=%q", roles(out), sysText(out))
	}

	// 顶层 system + messages 里的 system / developer：合并成一条，顶层在前；文本块数组也认。
	out, _, err = anthropicToChat(decode(t, `{"model":"m","system":"top","messages":[
	  {"role":"user","content":"hi"},
	  {"role":"system","content":[{"type":"text","text":"mid-1"},{"type":"text","text":"mid-2"}]},
	  {"role":"assistant","content":"ok"},
	  {"role":"developer","content":"dev"},
	  {"role":"user","content":"again"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if roles(out) != "system,user,assistant,user" {
		t.Fatalf("system 应合并并置于最前，其余顺序不变: %s", roles(out))
	}
	if got := sysText(out); got != "top\n\nmid-1\n\nmid-2\n\ndev" {
		t.Fatalf("合并后的 system = %q", got)
	}

	// 空的 system 消息不产生空的 system 条目。
	out, _, err = anthropicToChat(decode(t, `{"model":"m","messages":[
	  {"role":"system","content":"  "},{"role":"system","content":[]},{"role":"user","content":"hi"}]}`))
	if err != nil || roles(out) != "user" {
		t.Fatalf("空 system 应被忽略: err=%v roles=%v", err, out["messages"])
	}

	// 只有 system、没有任何对话内容：仍然要报错，而不是发一个只有 system 的请求给上游。
	if _, _, err = anthropicToChat(decode(t, `{"model":"m","messages":[{"role":"system","content":"x"}]}`)); err == nil {
		t.Fatal("没有 user / assistant 消息应报错")
	}
	// 真正未知的角色仍然拒绝。
	if _, _, err = anthropicToChat(decode(t, `{"model":"m","messages":[{"role":"robot","content":"x"},{"role":"user","content":"y"}]}`)); err == nil {
		t.Fatal("未知角色应继续拒绝")
	}
}
func TestResponsesToChat(t *testing.T) {
	in := decode(t, `{
	  "model":"gpt-x","instructions":"be brief","reasoning":{"effort":"high"},"max_output_tokens":500,
	  "text":{"format":{"type":"json_schema","name":"r","schema":{"type":"object"},"strict":true}},
	  "tools":[{"type":"function","name":"sh","parameters":{"type":"object"}},{"type":"web_search"}],
	  "input":[
	    {"role":"developer","content":"rules"},
	    {"type":"message","role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"data:image/png;base64,AA"}]},
	    {"type":"reasoning","summary":[{"type":"summary_text","text":"think"}]},
	    {"type":"function_call","call_id":"c1","name":"sh","arguments":"{\"cmd\":\"ls\"}"},
	    {"type":"function_call","call_id":"c2","name":"sh","arguments":"{}"},
	    {"type":"function_call_output","call_id":"c1","output":"a.txt"},
	    {"type":"function_call_output","call_id":"c2","output":"b.txt"}
	  ]}`)
	out, stream, err := responsesToChat(in)
	if err != nil || stream {
		t.Fatal(err)
	}
	msgs := out["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,system,user,assistant,tool,tool" {
		t.Fatalf("roles = %v", roles)
	}
	asst := msgs[3].(map[string]any)
	if len(asst["tool_calls"].([]any)) != 2 || asst["reasoning_content"] != "think" {
		t.Fatalf("assistant = %v", asst)
	}
	if out["reasoning_effort"] != "high" || out["max_tokens"] == nil || out["response_format"] == nil || len(out["tools"].([]any)) != 1 {
		t.Fatalf("out = %v", out)
	}
	if _, _, err := responsesToChat(decode(t, `{"model":"m","input":"x","previous_response_id":"r1"}`)); err == nil {
		t.Fatal("previous_response_id must be rejected")
	}
}

// 模拟 Provider 输出：思考 → 正文 → 两个工具调用（参数分片）→ 结束 → usage
const richSSE = `data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"let me "}}]}

data: {"id":"c","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}

data: {"id":"c","choices":[{"index":0,"delta":{"content":"Sure."}}]}

data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":"{\"p\":"}}]}}]}

data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}

data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"ls","arguments":"{}"}}]}}]}

data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"c","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":60},"completion_tokens_details":{"reasoning_tokens":5}}}

data: [DONE]

`

func feed(sink chatSink, contentType, body string, status int) {
	pw := newProtoWriter(httptest.NewRecorder(), sink)
	pw.Header().Set("Content-Type", contentType)
	pw.WriteHeader(status)
	// 分两段写入，验证跨块缓冲
	mid := len(body) / 3
	_, _ = pw.Write([]byte(body[:mid]))
	_, _ = pw.Write([]byte(body[mid:]))
	pw.Finish()
}

func sseEvents(body string) (names []string, datas []map[string]any) {
	for _, blk := range strings.Split(body, "\n\n") {
		var ev, data string
		for _, ln := range strings.Split(blk, "\n") {
			if v, ok := strings.CutPrefix(ln, "event: "); ok {
				ev = v
			}
			if v, ok := strings.CutPrefix(ln, "data: "); ok {
				data = v
			}
		}
		if data == "" {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(data), &m)
		names = append(names, ev)
		datas = append(datas, m)
	}
	return
}

func TestAnthropicStreamSink(t *testing.T) {
	rec := httptest.NewRecorder()
	feed(newAnthropicSink(rec, "glm-5.2", true), "text/event-stream", richSSE, 200)
	names, datas := sseEvents(rec.Body.String())
	want := "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop," +
		"content_block_start,content_block_delta,content_block_stop," +
		"content_block_start,content_block_delta,content_block_delta,content_block_stop," +
		"content_block_start,content_block_delta,content_block_stop,message_delta,message_stop"
	if strings.Join(names, ",") != want {
		t.Fatalf("events:\n%s\nwant\n%s", strings.Join(names, ","), want)
	}
	md := datas[len(datas)-2]
	if md["delta"].(map[string]any)["stop_reason"] != "tool_use" {
		t.Fatalf("stop = %v", md)
	}
	u := md["usage"].(map[string]any)
	if u["input_tokens"].(float64) != 40 || u["cache_read_input_tokens"].(float64) != 60 || u["output_tokens"].(float64) != 20 {
		t.Fatalf("usage = %v", u)
	}
	if cb := datas[8]["content_block"].(map[string]any); cb["type"] != "tool_use" || cb["id"] != "call_a" || cb["name"] != "read" {
		t.Fatalf("tool block = %v", cb)
	}
}

func TestAnthropicNonStreamSink(t *testing.T) {
	rec := httptest.NewRecorder()
	feed(newAnthropicSink(rec, "glm-5.2", false), "text/event-stream", richSSE, 200)
	m := decode(t, rec.Body.String())
	content := m["content"].([]any)
	if len(content) != 4 || m["stop_reason"] != "tool_use" {
		t.Fatalf("msg = %v", m)
	}
	tool := content[2].(map[string]any)
	if tool["input"].(map[string]any)["p"] != "x" {
		t.Fatalf("tool input = %v", tool)
	}
}

func TestResponsesStreamSink(t *testing.T) {
	rec := httptest.NewRecorder()
	feed(newResponsesSink(rec, "m", true, map[string]any{}), "text/event-stream", richSSE, 200)
	names, datas := sseEvents(rec.Body.String())
	if names[0] != "response.created" || names[len(names)-1] != "response.completed" {
		t.Fatalf("events = %v", names)
	}
	for i, d := range datas {
		if int(d["sequence_number"].(float64)) != i {
			t.Fatalf("sequence_number broken at %d", i)
		}
	}
	final := datas[len(datas)-1]["response"].(map[string]any)
	var types []string
	for _, it := range final["output"].([]any) {
		types = append(types, it.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "reasoning,message,function_call,function_call" {
		t.Fatalf("output = %v", types)
	}
	fc := final["output"].([]any)[2].(map[string]any)
	if fc["arguments"] != `{"p":"x"}` || fc["call_id"] != "call_a" {
		t.Fatalf("fc = %v", fc)
	}
	u := final["usage"].(map[string]any)
	if u["input_tokens"].(float64) != 100 || u["input_tokens_details"].(map[string]any)["cached_tokens"].(float64) != 60 {
		t.Fatalf("usage = %v", u)
	}
}

func TestSinkErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	feed(newAnthropicSink(rec, "m", true), "application/json", `{"error":{"message":"all accounts cooling","type":"rate_limit_error"}}`, 429)
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), `"type":"rate_limit_error"`) || !strings.Contains(rec.Body.String(), `"type":"error"`) {
		t.Fatalf("pre-stream error: %d %s", rec.Code, rec.Body.String())
	}
	// 流中错误帧
	rec = httptest.NewRecorder()
	feed(newResponsesSink(rec, "m", true, map[string]any{}), "text/event-stream",
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: {\"error\":{\"message\":\"boom\"}}\n\ndata: [DONE]\n\n", 200)
	names, _ := sseEvents(rec.Body.String())
	if names[len(names)-1] != "response.failed" {
		t.Fatalf("events = %v", names)
	}
}

func newStoreGateway(t *testing.T) (*http.ServeMux, *keys.Store, *requests.Recorder) {
	reg := provider.NewRegistry()
	_ = reg.Register(newFakeProvider())
	cfg := config.Default()
	cfg.Gateway.LegacyAPIKey = "k"
	g := New(func() *config.Config { return cfg }, reg, router.NewCatalog(reg, 0), nil)
	ks, err := keys.Open(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	rr := requests.New(requests.Config{})
	g.SetKeys(ks)
	g.SetRequests(rr)
	mux := http.NewServeMux()
	g.Register(mux)
	return mux, ks, rr
}

func do(mux *http.ServeMux, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestMessagesEndToEnd(t *testing.T) {
	mux, _, rr := newStoreGateway(t)
	rec := do(mux, "/v1/messages", `{"model":"mock-think","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`,
		map[string]string{"x-api-key": "k", "anthropic-version": "2023-06-01"})
	m := decode(t, rec.Body.String())
	if rec.Code != 200 || m["type"] != "message" || m["model"] != "mock-think" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if txt := m["content"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(txt, "hello") {
		t.Fatalf("text = %s", txt)
	}
	rec = do(mux, "/v1/messages", `{"model":"mock-think","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi there"}]}`,
		map[string]string{"x-api-key": "k"})
	names, _ := sseEvents(rec.Body.String())
	if names[0] != "message_start" || names[len(names)-1] != "message_stop" {
		t.Fatalf("stream events = %v", names)
	}
	rec = do(mux, "/v1/messages", `{"model":"nope","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`, map[string]string{"x-api-key": "k"})
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found_error"`) {
		t.Fatalf("unknown model: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(mux, "/v1/responses", `{"model":"mock-echo","input":"ping"}`, map[string]string{"Authorization": "Bearer k"})
	if m := decode(t, rec.Body.String()); rec.Code != 200 || m["object"] != "response" || m["status"] != "completed" {
		t.Fatalf("responses: %d %s", rec.Code, rec.Body.String())
	}
	got := rr.Query(requests.Filter{})
	if len(got) != 4 || got[0].Protocol != ProtoResponses || got[3].Protocol != ProtoMessages || got[1].Status != 404 {
		t.Fatalf("request log = %+v", got)
	}
}

// 端到端：Claude 经中转发来的请求，messages 里带 system 角色，网关应正常应答（此前返回 400 unsupported role "system"）。
func TestMessagesAcceptsSystemRoleEndToEnd(t *testing.T) {
	mux, _, _ := newStoreGateway(t)
	hdr := map[string]string{"x-api-key": "k", "anthropic-version": "2023-06-01"}
	for _, role := range []string{"system", "developer"} {
		body := `{"model":"mock-think","max_tokens":100,"messages":[{"role":"` + role + `","content":"you are a bot"},{"role":"user","content":"hello"}]}`
		rec := do(mux, "/v1/messages", body, hdr)
		if m := decode(t, rec.Body.String()); rec.Code != 200 || m["type"] != "message" {
			t.Fatalf("role=%s: %d %s", role, rec.Code, rec.Body.String())
		}
		rec = do(mux, "/v1/messages", strings.Replace(body, `"max_tokens":100`, `"max_tokens":100,"stream":true`, 1), hdr)
		names, _ := sseEvents(rec.Body.String())
		if rec.Code != 200 || len(names) == 0 || names[0] != "message_start" {
			t.Fatalf("role=%s stream: %d %s", role, rec.Code, rec.Body.String())
		}
	}
}
func TestKeyLimits(t *testing.T) {
	mux, ks, _ := newStoreGateway(t)
	k, err := ks.Create(keys.Spec{Name: "ci", Models: []string{"mock-echo"}, RPM: 2}, "")
	if err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"Authorization": "Bearer " + k.Secret}
	body := `{"model":"mock-echo","messages":[{"role":"user","content":"x"}]}`

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+k.Secret)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "mock-think") || !strings.Contains(rec.Body.String(), "mock-echo") {
		t.Fatalf("models not filtered: %s", rec.Body.String())
	}
	if rec := do(mux, "/v1/chat/completions", `{"model":"mock-think","messages":[]}`, auth); rec.Code != 403 {
		t.Fatalf("whitelist: %d", rec.Code)
	}
	for i := 0; i < 2; i++ {
		if rec := do(mux, "/v1/chat/completions", body, auth); rec.Code != 200 {
			t.Fatalf("req %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec = do(mux, "/v1/chat/completions", body, auth)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("rpm: %d %v", rec.Code, rec.Header())
	}
	got, _ := ks.Get(k.ID)
	if got.Usage.Requests != 4 || got.Usage.Failures != 2 || got.Usage.PromptTokens == 0 {
		t.Fatalf("usage = %+v", got.Usage) // 1 次白名单拒绝 + 2 次成功 + 1 次限流
	}

	// 预算耗尽
	b, _ := ks.Create(keys.Spec{Name: "budget", CreditBudget: 1}, "")
	ks.Record(b.ID, true, 0, 0, 0, 1.5)
	rec = do(mux, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer " + b.Secret})
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), "insufficient_quota") {
		t.Fatalf("budget: %d %s", rec.Code, rec.Body.String())
	}
	// 禁用
	off := false
	_, _ = ks.Update(b.ID, keys.Spec{Name: "budget", Enabled: &off})
	if rec := do(mux, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer " + b.Secret}); rec.Code != 401 {
		t.Fatalf("disabled: %d", rec.Code)
	}
}
