package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestAuth(t *testing.T) {
	e := newEnv(t, Config{})
	_, adminTok := e.user("root", service.RoleAdmin)
	alice, aliceTok := e.user("alice", service.RoleUser)

	if c := e.code("", "GET", "/api/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("no token: %d", c)
	}
	if c := e.code("ut_bad", "GET", "/api/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", c)
	}
	var me service.User
	if e.req(aliceTok, "GET", "/api/me", nil, &me); me.ID != alice.ID {
		t.Fatalf("me: %+v", me)
	}
	// 网页用 cookie
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/me", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: aliceTok})
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie: %d", resp.StatusCode)
	}
	if c := e.code(aliceTok, "GET", "/api/users", nil); c != http.StatusForbidden {
		t.Fatalf("user on admin api: %d", c)
	}
	if c := e.code(adminTok, "GET", "/api/users", nil); c != http.StatusOK {
		t.Fatalf("admin api: %d", c)
	}
	// 带 X-Agentuse-User 但就是自己：不算代为
	if r := e.req(aliceTok, "GET", "/api/me", nil, nil, actAsHeader, alice.ID); r.StatusCode != http.StatusOK {
		t.Fatalf("act as self: %d", r.StatusCode)
	}
	if r := e.req(adminTok, "GET", "/api/me", nil, nil, actAsHeader, "has space"); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("act as invalid id: %d", r.StatusCode)
	}
}

func TestActAsUser(t *testing.T) {
	e := newEnv(t, Config{})
	_, adminTok := e.user("root", service.RoleAdmin)
	alice, aliceTok, _ := e.st.CreateUser(context.Background(), "alice", "alice", service.RoleUser)

	// 不存在的用户自动建
	var me service.User
	if r := e.req(adminTok, "GET", "/api/me", nil, &me, actAsHeader, "u_10086"); r.StatusCode != 200 || me.ID != "u_10086" || me.Role != service.RoleUser {
		t.Fatalf("act as new user: %d %+v", r.StatusCode, me)
	}
	// 代为 alice 添加的 daemon 挂在 alice 下
	var d service.Daemon
	if r := e.req(adminTok, "POST", "/api/daemons", map[string]string{"name": "x"}, &d, actAsHeader, alice.ID); r.StatusCode != http.StatusCreated || d.UserID != alice.ID {
		t.Fatalf("create daemon as alice: %d %+v", r.StatusCode, d)
	}
	// 代为用户时就是普通用户权限
	if r := e.req(adminTok, "GET", "/api/users", nil, nil, actAsHeader, alice.ID); r.StatusCode != http.StatusForbidden {
		t.Fatalf("admin api as user: %d", r.StatusCode)
	}
	// 普通用户令牌不能带
	if r := e.req(aliceTok, "GET", "/api/me", nil, nil, actAsHeader, "u_10086"); r.StatusCode != http.StatusForbidden {
		t.Fatalf("user token with header: %d", r.StatusCode)
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t, Config{})
	alice, tok := e.user("alice", service.RoleUser)

	var u service.User
	resp := e.req("", "POST", "/api/login", map[string]string{"token": " " + tok + " "}, &u)
	if resp.StatusCode != 200 || u.ID != alice.ID || cookie(resp) == nil || cookie(resp).Value != tok || !cookie(resp).HttpOnly {
		t.Fatalf("login: %d %+v %+v", resp.StatusCode, u, cookie(resp))
	}
	if c := e.code("", "POST", "/api/login", map[string]string{"token": "ut_bad"}); c != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", c)
	}
	if c := e.code("", "POST", "/api/login", "{"); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	if resp := e.req("", "POST", "/api/logout", nil, nil); resp.StatusCode != http.StatusNoContent || cookie(resp).MaxAge >= 0 {
		t.Fatalf("logout: %d %+v", resp.StatusCode, cookie(resp))
	}

	// 启动时打印的登录链接
	resp = e.req("", "GET", "/login?token="+tok, nil, nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" || cookie(resp).Value != tok {
		t.Fatalf("login link: %d %v", resp.StatusCode, resp.Header)
	}
	resp = e.req("", "GET", "/login?token=ut_bad", nil, nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/?login=invalid" || cookie(resp) != nil {
		t.Fatalf("bad login link: %d %v", resp.StatusCode, resp.Header)
	}
	// https 部署时 cookie 带 Secure
	resp = e.req("", "POST", "/api/login", map[string]string{"token": tok}, nil, "X-Forwarded-Proto", "https")
	if !cookie(resp).Secure {
		t.Fatal("cookie should be Secure behind https")
	}
}

func cookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	return nil
}

func TestUsersAPI(t *testing.T) {
	e := newEnv(t, Config{PublicURL: "https://agentuse.example.com"})
	_, adminTok := e.user("root", service.RoleAdmin)

	var created struct {
		User     service.User `json:"user"`
		Token    string       `json:"token"`
		LoginURL string       `json:"login_url"`
	}
	if r := e.req(adminTok, "POST", "/api/users", map[string]string{"id": "u_1", "name": "alice"}, &created); r.StatusCode != http.StatusCreated ||
		created.User.Role != service.RoleUser || created.LoginURL != "https://agentuse.example.com/login?token="+created.Token {
		t.Fatalf("create: %d %+v", r.StatusCode, created)
	}
	if c := e.code(adminTok, "POST", "/api/users", map[string]string{"name": "alice"}); c != http.StatusBadRequest {
		t.Fatalf("duplicate name: %d", c)
	}
	if c := e.code(adminTok, "POST", "/api/users", "nope"); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	old := created.Token
	if r := e.req(adminTok, "POST", "/api/users/u_1/token", nil, &created); r.StatusCode != 200 || created.Token == old {
		t.Fatalf("reset: %d %+v", r.StatusCode, created)
	}
	if c := e.code(old, "GET", "/api/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("old token: %d", c)
	}
	if c := e.code(adminTok, "POST", "/api/users/nobody/token", nil); c != http.StatusNotFound {
		t.Fatalf("reset unknown: %d", c)
	}
	var users []service.User
	if e.req(adminTok, "GET", "/api/users", nil, &users); len(users) != 2 {
		t.Fatalf("list: %+v", users)
	}
}

func TestHarnessesAPI(t *testing.T) {
	e := newEnv(t, Config{})
	_, aliceTok := e.user("alice", service.RoleUser)
	_, bobTok := e.user("bob", service.RoleUser)

	var h service.Harness
	if r := e.req(aliceTok, "POST", "/api/harnesses", map[string]string{"name": "mine", "description": "d"}, &h); r.StatusCode != http.StatusCreated || h.Kind != service.HarnessCustom {
		t.Fatalf("create: %d %+v", r.StatusCode, h)
	}
	if c := e.code(aliceTok, "POST", "/api/harnesses", map[string]string{"name": "Bad Name"}); c != http.StatusBadRequest {
		t.Fatalf("bad name: %d", c)
	}
	var hs []service.Harness
	if e.req(bobTok, "GET", "/api/harnesses", nil, &hs); len(hs) != 3 {
		t.Fatalf("list: %+v", hs)
	}
	if c := e.code(bobTok, "DELETE", "/api/harnesses/mine", nil); c != http.StatusNotFound {
		t.Fatalf("bob deletes: %d", c)
	}
	if c := e.code(aliceTok, "DELETE", "/api/harnesses/mine", nil); c != http.StatusNoContent {
		t.Fatalf("delete: %d", c)
	}
}

// daemonWith 建一台 u 的、已上报并开启 harnesses 的 daemon。
func (e *env) daemonWith(u service.User, harnesses ...string) service.Daemon {
	e.t.Helper()
	ctx := context.Background()
	d, _ := e.st.CreateDaemon(ctx, u.ID, "laptop")
	d, _, _ = e.st.RegisterDaemon(ctx, d.SetupToken, "")
	var rep []service.ReportedHarness
	for _, h := range harnesses {
		rep = append(rep, service.ReportedHarness{Name: h})
	}
	e.st.DaemonConnected(ctx, d, rep)
	for _, h := range harnesses {
		e.st.SetDaemonHarnessEnabled(ctx, u, d.ID, h, true)
	}
	return d
}

func TestAgentsAPI(t *testing.T) {
	e := newEnv(t, Config{})
	alice, aliceTok := e.user("alice", service.RoleUser)
	_, bobTok := e.user("bob", service.RoleUser)
	d := e.daemonWith(alice, "claude", "codex")

	var a service.Agent
	if r := e.req(aliceTok, "POST", "/api/agents", map[string]string{"id": "coder", "name": "写代码", "daemon_id": d.ID, "harness": "claude"}, &a); r.StatusCode != http.StatusCreated || a.ID != "coder" {
		t.Fatalf("create: %d %+v", r.StatusCode, a)
	}
	if c := e.code(aliceTok, "POST", "/api/agents", map[string]string{"name": "x", "daemon_id": d.ID, "harness": "ghost"}); c != http.StatusBadRequest {
		t.Fatalf("bad runtime: %d", c)
	}
	if c := e.code(aliceTok, "POST", "/api/agents", "["); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	if c := e.code(bobTok, "GET", "/api/agents/coder", nil); c != http.StatusNotFound {
		t.Fatalf("bob get: %d", c)
	}
	// PATCH:没给的字段不变
	if r := e.req(aliceTok, "PATCH", "/api/agents/coder", map[string]string{"description": "新说明"}, &a); r.StatusCode != 200 || a.Name != "写代码" || a.Description != "新说明" {
		t.Fatalf("patch description: %d %+v", r.StatusCode, a)
	}
	if c := e.code(aliceTok, "PATCH", "/api/agents/coder", map[string]string{"harness": "codex"}); c != http.StatusBadRequest {
		t.Fatalf("harness alone: %d", c)
	}
	if r := e.req(aliceTok, "PATCH", "/api/agents/coder", map[string]string{"daemon_id": d.ID, "harness": "codex"}, &a); r.StatusCode != 200 || a.Harness != "codex" {
		t.Fatalf("switch runtime: %d %+v", r.StatusCode, a)
	}
	if c := e.code(aliceTok, "PATCH", "/api/agents/coder", "x"); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	var list []service.Agent
	if e.req(aliceTok, "GET", "/api/agents", nil, &list); len(list) != 1 {
		t.Fatalf("list: %+v", list)
	}
	if e.req(bobTok, "GET", "/api/agents", nil, &list); len(list) != 0 {
		t.Fatalf("bob list: %+v", list)
	}
	if c := e.code(aliceTok, "GET", "/api/agents/coder", nil); c != http.StatusOK {
		t.Fatalf("get: %d", c)
	}
	if c := e.code(bobTok, "DELETE", "/api/agents/coder", nil); c != http.StatusNotFound {
		t.Fatalf("bob delete: %d", c)
	}
	if c := e.code(aliceTok, "DELETE", "/api/agents/coder", nil); c != http.StatusNoContent {
		t.Fatalf("delete: %d", c)
	}
}

func TestDaemonsAPI(t *testing.T) {
	e := newEnv(t, Config{})
	_, aliceTok := e.user("alice", service.RoleUser)
	_, bobTok := e.user("bob", service.RoleUser)

	// 不带请求体也能建
	var v daemonView
	if r := e.req(aliceTok, "POST", "/api/daemons", nil, &v); r.StatusCode != http.StatusCreated || v.Name != "" ||
		v.SetupCommand != `curl -fsSL "`+e.ts.URL+`/setup.sh" -o setup.sh && TOKEN="`+v.SetupToken+`" bash setup.sh` {
		t.Fatalf("create: %d %+v", r.StatusCode, v)
	}
	if c := e.code(aliceTok, "POST", "/api/daemons", "{"); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	var list []daemonView
	if e.req(aliceTok, "GET", "/api/daemons", nil, &list); len(list) != 1 || list[0].SetupCommand == "" {
		t.Fatalf("list: %+v", list)
	}

	// 注册后不再有安装命令
	var reg protocol.RegisterResponse
	if r := e.req("", "POST", "/api/daemon/register", map[string]string{"token": v.SetupToken, "hostname": "host"}, &reg); r.StatusCode != 200 || reg.DaemonID != v.ID {
		t.Fatalf("register: %d %+v", r.StatusCode, reg)
	}
	var after []daemonView
	if e.req(aliceTok, "GET", "/api/daemons", nil, &after); after[0].SetupCommand != "" || after[0].SetupToken != "" || after[0].Name != "host" {
		t.Fatalf("after register: %+v", after)
	}
	if c := e.code("", "POST", "/api/daemon/register", map[string]string{"token": v.SetupToken}); c != http.StatusUnauthorized {
		t.Fatalf("reuse setup token: %d", c)
	}
	if c := e.code("", "POST", "/api/daemon/register", "x"); c != http.StatusBadRequest {
		t.Fatalf("register bad json: %d", c)
	}
	if c := e.code(reg.Key, "GET", "/api/daemon/me", nil); c != http.StatusOK {
		t.Fatalf("daemon me: %d", c)
	}
	if c := e.code("", "GET", "/api/daemon/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("daemon me without key: %d", c)
	}

	// 开关只能开上报过的
	if c := e.code(aliceTok, "PUT", "/api/daemons/"+v.ID+"/harnesses/claude", map[string]bool{"enabled": true}); c != http.StatusNotFound {
		t.Fatalf("toggle unreported: %d", c)
	}
	if c := e.code(aliceTok, "PUT", "/api/daemons/"+v.ID+"/harnesses/claude", "x"); c != http.StatusBadRequest {
		t.Fatalf("toggle bad json: %d", c)
	}
	d, _ := e.st.DaemonByKey(context.Background(), reg.Key)
	e.st.DaemonConnected(context.Background(), d, []service.ReportedHarness{{Name: "claude"}})
	if r := e.req(aliceTok, "PUT", "/api/daemons/"+v.ID+"/harnesses/claude", map[string]bool{"enabled": true}, &v); r.StatusCode != 200 || !v.Harnesses[0].Enabled {
		t.Fatalf("toggle: %d %+v", r.StatusCode, v)
	}

	// 重置：新安装命令，旧密钥失效
	if c := e.code(bobTok, "POST", "/api/daemons/"+v.ID+"/reset", nil); c != http.StatusNotFound {
		t.Fatalf("bob reset: %d", c)
	}
	if r := e.req(aliceTok, "POST", "/api/daemons/"+v.ID+"/reset", nil, &v); r.StatusCode != 200 || v.SetupCommand == "" || v.Status != service.DaemonPending {
		t.Fatalf("reset: %d %+v", r.StatusCode, v)
	}
	if c := e.code(reg.Key, "GET", "/api/daemon/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("old key after reset: %d", c)
	}

	if c := e.code(bobTok, "DELETE", "/api/daemons/"+v.ID, nil); c != http.StatusNotFound {
		t.Fatalf("bob delete: %d", c)
	}
	if c := e.code(aliceTok, "DELETE", "/api/daemons/"+v.ID, nil); c != http.StatusNoContent {
		t.Fatalf("delete: %d", c)
	}
}

func TestMessageContent(t *testing.T) {
	if c, err := messageContent(json.RawMessage(`{"a":1}`), "ignored"); err != nil || string(c) != `{"a":1}` {
		t.Fatalf("input wins: %s %v", c, err)
	}
	if c, err := messageContent(nil, `say "hi"`); err != nil || string(c) != `{"prompt":"say \"hi\""}` {
		t.Fatalf("prompt: %s %v", c, err)
	}
	if _, err := messageContent(nil, " \n"); err == nil {
		t.Fatal("empty should fail")
	}
	if firstRunes("中文abc", 3) != "中文a" || firstRunes("ab", 3) != "ab" {
		t.Fatal("firstRunes")
	}
}
