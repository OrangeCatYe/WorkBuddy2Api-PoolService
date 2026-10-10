package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/core/provider"
	"github.com/orangeruan/poolservice/internal/providers/workbuddy/engine/auth"
)

// 设备授权端点与请求头（与原项目 cmd/login、panel/login.go 一致）。
const (
	upstreamBaseCN      = "https://copilot.tencent.com"
	upstreamBaseGlobal  = "https://www.workbuddy.ai"
	loginUA             = "CLI/2.63.2 CodeBuddy/2.63.2"
	originRefererCN     = "https://www.codebuddy.cn"
	originRefererGlobal = "https://www.workbuddy.ai"
	loginTTL            = 10 * time.Minute
)

type loginSession struct {
	created time.Time
	realm   string
}

var loginHTTP = &http.Client{Timeout: 30 * time.Second}

// LoginMethods 支持的添加账号方式。
func (p *Provider) LoginMethods() []provider.LoginMethod {
	methods := []provider.LoginMethod{
		{ID: "oauth_cn", Name: "国内版授权", Description: "打开 CodeBuddy 授权页登录，完成后自动加入账号池"},
	}
	if p.cfg.Global.Enabled {
		methods = append(methods, provider.LoginMethod{
			ID: "oauth_global", Name: "国际版授权", Description: "WorkBuddy 国际版（workbuddy.ai）授权登录",
		})
	}
	methods = append(methods, provider.LoginMethod{
		ID: "import_json", Name: "导入凭证",
		Description: "粘贴单个凭证 JSON（auths/*.json 格式）或 cockpit tools 导出的账号数组",
		Fields:      []provider.FieldDef{{Key: "json", Label: "凭证 JSON", Kind: "textarea"}},
	})
	return methods
}

func loginEndpoints(realm string) (state, token, account, origin string) {
	base, origin := upstreamBaseCN, originRefererCN
	if realm == "global" {
		base, origin = upstreamBaseGlobal, originRefererGlobal
	}
	return base + "/v2/plugin/auth/state?platform=CLI",
		base + "/v2/plugin/auth/token?state=",
		base + "/v2/plugin/login/account?state=",
		origin
}

func loginJSON(ctx context.Context, method, url, bearer string, body io.Reader, origin string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", loginUA)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := loginHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream http %d", resp.StatusCode)
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("code=%d msg=%s", env.Code, env.Msg)
	}
	return env.Data, nil
}

// StartLogin 发起登录。OAuth 返回授权 URL 由前端打开并轮询；导入方式同步完成。
func (p *Provider) StartLogin(ctx context.Context, method string, params map[string]string) (*provider.LoginSession, *provider.LoginStatus, error) {
	switch method {
	case "oauth_cn", "oauth_global":
		realm := "cn"
		if method == "oauth_global" {
			if !p.cfg.Global.Enabled {
				return nil, nil, errors.New("国际版已在配置中关闭（providers.workbuddy.global.enabled=false）")
			}
			realm = "global"
		}
		epState, _, _, origin := loginEndpoints(realm)
		data, err := loginJSON(ctx, http.MethodPost, epState, "", bytes.NewReader([]byte("{}")), origin)
		if err != nil {
			return nil, nil, fmt.Errorf("获取授权链接失败: %w", err)
		}
		var st struct {
			State   string `json:"state"`
			AuthURL string `json:"authUrl"`
		}
		if err := json.Unmarshal(data, &st); err != nil || st.State == "" || st.AuthURL == "" {
			return nil, nil, errors.New("上游未返回授权链接")
		}
		p.loginMu.Lock()
		for k, s := range p.logins {
			if time.Since(s.created) > loginTTL {
				delete(p.logins, k)
			}
		}
		p.logins[st.State] = loginSession{created: time.Now(), realm: realm}
		p.loginMu.Unlock()
		log.Printf("[workbuddy] 发起 OAuth 添加账号 realm=%s", realm)
		return &provider.LoginSession{ID: st.State, AuthURL: st.AuthURL, ExpiresAt: time.Now().Add(loginTTL)}, nil, nil

	case "import_json":
		res, err := p.importJSON(ctx, []byte(params["json"]))
		if err != nil {
			return nil, nil, err
		}
		return nil, res, nil
	}
	return nil, nil, fmt.Errorf("unknown login method %q", method)
}

// PollLogin 轮询 OAuth 结果；完成后落盘凭证、热加载进池并顺带签到 / 激活。
func (p *Provider) PollLogin(ctx context.Context, sessionID string) (*provider.LoginStatus, error) {
	p.loginMu.Lock()
	sess, ok := p.logins[sessionID]
	p.loginMu.Unlock()
	if !ok || time.Since(sess.created) > loginTTL {
		return &provider.LoginStatus{State: "expired", Message: "授权会话已过期，请重新发起"}, nil
	}
	_, epToken, epAcct, origin := loginEndpoints(sess.realm)
	tokRaw, err := loginJSON(ctx, http.MethodGet, epToken+sessionID, "", nil, origin)
	if err != nil {
		return &provider.LoginStatus{State: "pending", Message: "等待授权完成"}, nil
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(tokRaw, &tok); err != nil || tok.AccessToken == "" {
		return &provider.LoginStatus{State: "pending", Message: "等待授权完成"}, nil
	}
	var acct struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	}
	if raw, err := loginJSON(ctx, http.MethodGet, epAcct+sessionID, tok.AccessToken, nil, origin); err == nil {
		_ = json.Unmarshal(raw, &acct)
	}
	if !validUID(acct.UID) {
		return &provider.LoginStatus{State: "failed", Message: "已授权但未取到合法的账号 UID，请重试"}, nil
	}
	a := &auth.Auth{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
		Domain:       tok.Domain,
		UID:          acct.UID,
		EnterpriseID: acct.EnterpriseID,
		Nickname:     acct.Nickname,
	}
	msg, err := p.adopt(a, sess.realm)
	if err != nil {
		return &provider.LoginStatus{State: "failed", Message: err.Error()}, nil
	}
	p.loginMu.Lock()
	delete(p.logins, sessionID)
	p.loginMu.Unlock()
	name := acct.Nickname
	if name == "" {
		name = shortUID(acct.UID)
	}
	return &provider.LoginStatus{State: "success", AccountID: acct.UID, Message: joinMsg("已添加 "+name, msg)}, nil
}

// adopt 落盘凭证、加入池并执行首登动作（签到 / 国际版激活 + trial）。
// 返回附加提示文案；失败只影响凭证落盘时返回 error。
func (p *Provider) adopt(a *auth.Auth, realm string) (string, error) {
	a.FilePath = filepath.Join(p.cfg.AuthDir, fmt.Sprintf("workbuddy-%s.json", a.UID))
	if realm == "global" {
		if _, err := auth.BackfillRealmFor(a, "global"); err != nil {
			return "", fmt.Errorf("set realm: %w", err)
		}
	} else {
		_, _ = a.BackfillRealm()
	}
	if err := a.SaveAtomic(); err != nil {
		return "", fmt.Errorf("保存凭证失败: %w", err)
	}
	p.pool.Add(a)
	p.pool.Revive(a.UID)

	var notes []string
	if realm == "global" {
		if _, err := p.up.GlobalCompleteRegistration(a); err != nil {
			notes = append(notes, "注册激活失败: "+err.Error())
		}
		if _, err := p.up.ClaimTrial(a); err != nil {
			notes = append(notes, "trial 领取失败: "+err.Error())
		}
	} else if !a.IsEnterprise() {
		if err := p.up.DailyCheckin(a); err != nil {
			notes = append(notes, "签到: "+err.Error())
		} else {
			p.pool.NoteCheckinDone(a.UID)
		}
	}
	if rm, tt, err := p.up.UserResource(a); err == nil {
		p.pool.SetCredits(a.UID, rm, tt)
		p.pool.ReenableIfCredits(a.UID, rm, tt)
	}
	go p.refreshPackages(a.UID)
	log.Printf("[workbuddy] 新账号已热加载 uid=%s realm=%s", shortUID(a.UID), realm)
	return joinMsg(notes...), nil
}

// cockpitAccount cockpit tools 导出格式。
type cockpitAccount struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	UID          string `json:"uid"`
	Nickname     string `json:"nickname"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // 毫秒
	Domain       string `json:"domain"`
}

// importJSON 支持：单个凭证（嵌套 / 扁平形，同 auths/*.json）、凭证数组、cockpit 数组。
func (p *Provider) importJSON(_ context.Context, raw []byte) (*provider.LoginStatus, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("请粘贴凭证 JSON")
	}
	var items []json.RawMessage
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("JSON 解析失败: %w", err)
		}
	} else {
		items = []json.RawMessage{raw}
	}
	var ok int
	var errs []string
	for i, it := range items {
		a, err := parseImported(it)
		if err != nil {
			errs = append(errs, fmt.Sprintf("#%d %v", i+1, err))
			continue
		}
		realm := a.Realm()
		if _, err := p.adopt(a, realm); err != nil {
			errs = append(errs, fmt.Sprintf("#%d %v", i+1, err))
			continue
		}
		ok++
	}
	st := &provider.LoginStatus{State: "success", Message: fmt.Sprintf("导入 %d / %d 个账号", ok, len(items))}
	if ok == 0 {
		st.State = "failed"
	}
	if len(errs) > 0 {
		st.Message = joinMsg(st.Message, strings.Join(errs, "；"))
	}
	return st, nil
}

func parseImported(raw json.RawMessage) (*auth.Auth, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if _, isCockpit := probe["access_token"]; isCockpit {
		var c cockpitAccount
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		if c.UID == "" || c.AccessToken == "" || c.RefreshToken == "" {
			return nil, errors.New("缺少 uid / access_token / refresh_token")
		}
		exp := c.ExpiresAt / 1000
		if exp <= 0 {
			exp = time.Now().Add(365 * 24 * time.Hour).Unix()
		}
		nick := c.Nickname
		if nick == "" {
			nick = c.Email
		}
		a := &auth.Auth{AccessToken: c.AccessToken, RefreshToken: c.RefreshToken, ExpiresAt: exp,
			Domain: c.Domain, UID: strings.TrimSpace(c.UID), Nickname: nick}
		if !validUID(a.UID) {
			return nil, errors.New("uid 含非法字符")
		}
		if auth.ResolveRealm("", c.Domain) == "global" {
			_, _ = auth.BackfillRealmFor(a, "global")
		}
		return a, nil
	}
	a, err := auth.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !validUID(a.UID) {
		return nil, errors.New("缺少 uid 或 uid 含非法字符")
	}
	return a, nil
}

// validUID 只放行 [A-Za-z0-9_-]，防止上游 / 导入数据造成路径穿越。
func validUID(uid string) bool {
	if uid == "" || len(uid) > 64 {
		return false
	}
	for _, c := range uid {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func joinMsg(parts ...string) string {
	var out []string
	for _, s := range parts {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "；")
}
