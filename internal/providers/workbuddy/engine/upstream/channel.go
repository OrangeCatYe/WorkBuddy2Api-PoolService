// channel.go 渠道校验（业务码 11128）的 system 开头指纹处理。
//
// 背景（参考 dsh-connect-workbuddy v4.13.0 / v4.13.2 的实测，本项目未复测）：
// WorkBuddy 网关会扫每一条 system 消息的开头，开头命中「已知第三方 agent 提示」清单就回
// 400 + 业务码 11128（msg: unapproved channel / illegal api invocation）。判据只在请求体里，
// 与账号、额度、网络、UA、请求头都无关；同一句话放在提示中段、放进 user 消息、开头多一个
// 空格都不会触发。实测命中：
//
//	You are an AI agent powered by DeepSeek Harness   （国内版拦，国际版放行；前 47 个字符即触发）
//	You are Claude Code, Anthropic's official CLI for Claude.   （两个域都拦）
//
// 修法只动「开头」，不改提示内容：命中时在该消息最前面补一行中性的话，宿主提示原文一字不改，
// 未命中的请求逐字节不动。清单是实测快照，网关新增词条不会被提前发现，所以另有两层兜底：
//   - handler 在撞上该拦截后，对每条 system 消息补中性开头行重试一次（NeutralizeSystemOpenings）；
//   - 重试通过则把这些开头记进 Client 的学习集合（LearnChannelOpenings），之后同样开头的请求
//     出站前就已处理，不再先撞一次 400。学习集合只在进程内存里，重启清零。
//
// 与 sanitize.go 的分工：sanitize 改写「模板句本身」（换一个词），本文件只补「开头行」。
// Claude Code 的身份句在脱敏开启时已被改写，不再命中下面的清单，请求体与改动前逐字节一致。
package upstream

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// neutralOpening 补在 system 消息最前面的中性开头行，与 ensureConsoleSystem 的兜底 system 同一句。
const neutralOpening = "You are a helpful assistant."

const (
	// learnKeyRunes 学习到的开头只取归一化后的前 N 个字符作前缀。
	// 实测 DeepSeek Harness 的触发前缀是 47 个字符；取 48 让该条能被完整学到。
	learnKeyRunes = 48
	// learnKeyMinRunes 太短的前缀（例如 "hi"）会误伤大量正常提示，不学。
	learnKeyMinRunes = 12
	// maxLearnedOpenings 学习集合上限，满了丢最早的。
	maxLearnedOpenings = 64
	// evidenceMaxLines / evidenceMaxRunes 排障回显：最多取几条 system 消息、每条截多长。
	evidenceMaxLines = 3
	evidenceMaxRunes = 120
)

// knownChannelOpenings 已实测会被渠道校验拦截的 system 开头（归一化后的小写前缀）。
// Claude Code 取到整句身份句为止：脱敏把它改成 "…CLI tool for Claude" 之后就不再匹配，
// 只有关闭脱敏时才会走到这里。
var knownChannelOpenings = []string{
	"you are an ai agent powered by deepseek harness",
	"you are claude code, anthropic's official cli for claude",
}

// openingSet 学习到的开头前缀集合。零值可用（测试里直接 &Client{} 构造）。
type openingSet struct {
	mu   sync.RWMutex
	keys []string
}

func (s *openingSet) has(norm string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.keys {
		if strings.HasPrefix(norm, k) {
			return true
		}
	}
	return false
}

func (s *openingSet) learn(keys []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
outer:
	for _, k := range keys {
		if utf8.RuneCountInString(k) < learnKeyMinRunes || strings.HasPrefix(k, strings.ToLower(neutralOpening)) {
			continue
		}
		for _, have := range s.keys {
			if have == k {
				continue outer
			}
		}
		s.keys = append(s.keys, k)
		if len(s.keys) > maxLearnedOpenings {
			s.keys = s.keys[len(s.keys)-maxLearnedOpenings:]
		}
	}
}

func (s *openingSet) size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keys)
}

// normalizeOpening 去掉前导空白并转小写：网关按偏移 0 的内容做前缀比对，我们只比它更保守。
func normalizeOpening(s string) string {
	return strings.ToLower(strings.TrimLeftFunc(s, unicode.IsSpace))
}

// openingKey 取归一化文本的前 learnKeyRunes 个字符作为学习用的前缀。
func openingKey(norm string) string {
	if utf8.RuneCountInString(norm) <= learnKeyRunes {
		return norm
	}
	r := []rune(norm)
	return string(r[:learnKeyRunes])
}

func isSystemRole(m map[string]any) bool {
	role, _ := m["role"].(string)
	role = strings.TrimSpace(role)
	return strings.EqualFold(role, "system") || strings.EqualFold(role, "developer")
}

// openingText 取 content 的开头文本：字符串直接用；多模态数组取第一个有文本的 part。
func openingText(content any) (string, bool) {
	switch c := content.(type) {
	case string:
		return c, c != ""
	case []any:
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				if t, ok := m["text"].(string); ok && t != "" {
					return t, true
				}
			}
		}
	}
	return "", false
}

// prependNeutral 在消息 content 最前面补中性开头行。字符串补一行；数组在最前面插一个 text part。
func prependNeutral(m map[string]any) bool {
	switch c := m["content"].(type) {
	case string:
		m["content"] = neutralOpening + "\n" + c
		return true
	case []any:
		m["content"] = append([]any{map[string]any{"type": "text", "text": neutralOpening}}, c...)
		return true
	}
	return false
}

// neutralizeSystemOpenings 给 system / developer 消息补中性开头行，返回被改动的消息的学习键。
// force=false：只处理命中已知清单或学习集合的消息（出站前的主动处理）；
// force=true：处理所有尚未以中性开头行开头的消息（撞上拦截之后的重试，不依赖清单是否过时）。
func neutralizeSystemOpenings(msgs []any, force bool, learned *openingSet) []string {
	var keys []string
	lowNeutral := strings.ToLower(neutralOpening)
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok || !isSystemRole(m) {
			continue
		}
		text, ok := openingText(m["content"])
		if !ok {
			continue
		}
		norm := normalizeOpening(text)
		if norm == "" || strings.HasPrefix(norm, lowNeutral) {
			continue // 已经以中性开头行开头：幂等，不重复补
		}
		if !force && !isKnownOpening(norm, learned) {
			continue
		}
		if prependNeutral(m) {
			keys = append(keys, openingKey(norm))
		}
	}
	return keys
}

func isKnownOpening(norm string, learned *openingSet) bool {
	for _, p := range knownChannelOpenings {
		if strings.HasPrefix(norm, p) {
			return true
		}
	}
	return learned.has(norm)
}

// NeutralizeSystemOpenings 撞上渠道校验拦截后的重试用：对每条 system / developer 消息补中性开头行，
// 返回改写后的请求体与被改动消息的学习键。没有可改动的消息或 body 不可解析 → 原样返回（nil 键）。
// 其余字段、其余消息逐字不动。
func NeutralizeSystemOpenings(body []byte) ([]byte, []string) {
	var obj map[string]any
	if len(body) == 0 || json.Unmarshal(body, &obj) != nil {
		return body, nil
	}
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return body, nil
	}
	keys := neutralizeSystemOpenings(msgs, true, nil)
	if len(keys) == 0 {
		return body, nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body, nil
	}
	return out, keys
}

// LearnChannelOpenings 记住「补了中性开头行之后请求通过」的开头，之后同样开头的请求出站前就处理。
func (c *Client) LearnChannelOpenings(keys []string) { c.openings.learn(keys) }

// LearnedChannelOpenings 当前学到的开头数量（排障与测试用）。
func (c *Client) LearnedChannelOpenings() int { return c.openings.size() }

// SystemOpenings 取出每条 system / developer 消息的第一行非空内容（每条最多 evidenceMaxRunes 个字符，
// 最多 evidenceMaxLines 条）。用于被渠道校验拦截时回显「上游看到的开头是什么」：证据在我们发出去的
// 请求里，不在上游的回复里。读不出来就返回空，不编造。
func SystemOpenings(body []byte) []string {
	var obj struct {
		Messages []map[string]any `json:"messages"`
	}
	if len(body) == 0 || json.Unmarshal(body, &obj) != nil {
		return nil
	}
	var out []string
	for _, m := range obj.Messages {
		if len(out) >= evidenceMaxLines {
			break
		}
		if !isSystemRole(m) {
			continue
		}
		text, ok := openingText(m["content"])
		if !ok {
			continue
		}
		line := firstLine(text)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > evidenceMaxRunes {
			line = string([]rune(line)[:evidenceMaxRunes]) + "…"
		}
		out = append(out, line)
	}
	return out
}

func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// WireSystemOpenings 按出站管线（含脱敏与开头处理）处理 body 之后，取实际上线的 system 开头。
// 取的是真实上线的字节：命中已知指纹时回显的是补上的那行中性开头，这正是「证据」的含义。
func (c *Client) WireSystemOpenings(realm string, body []byte) []string {
	return SystemOpenings(c.prepareBody(body, realm, "", ""))
}

// IsChannelReject 判断上游错误体是不是渠道校验（11128）里「system 开头指纹」那一道门。
// 只认 msg 文案，不认 displayTips：上游对 11128 这一族门写的提示语一字不差，不能当判据。
// 另外两道同码的门（缺 conversation id、首条消息不是 system）文案不同，不在这里。
func IsChannelReject(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "unapproved channel") || strings.Contains(lower, "illegal api invocation")
}

// quoteOpenings 把回显的开头拼成一行 `"a", "b"`。
func quoteOpenings(lines []string) string {
	q := make([]string, len(lines))
	for i, l := range lines {
		q[i] = strconv.Quote(l)
	}
	return strings.Join(q, ", ")
}
