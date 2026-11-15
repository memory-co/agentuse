package controller

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	deviceservice "github.com/memory-co/agentuse/server/internal/service/device"
)

func TestAuth(t *testing.T) {
	e := newEnv(t, Config{})
	_, adminTok := e.user("root", agentuse.RoleAdmin)
	alice, aliceTok := e.user("alice", agentuse.RoleUser)

	if c := e.code("", "GET", "/api/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("no token: %d", c)
	}
	if c := e.code("ut_bad", "GET", "/api/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", c)
	}
	var me agentuse.User
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
	_, adminTok := e.user("root", agentuse.RoleAdmin)
	alice, aliceTok, _ := e.st.UserService.CreateUser(context.Background(), "alice", "alice", agentuse.RoleUser)

	// 不存在的用户自动建
	var me agentuse.User
	if r := e.req(adminTok, "GET", "/api/me", nil, &me, actAsHeader, "u_10086"); r.StatusCode != 200 || me.ID != "u_10086" || me.Role != agentuse.RoleUser {
		t.Fatalf("act as new user: %d %+v", r.StatusCode, me)
	}
	// 代为 alice 添加的 device 挂在 alice 下
	var d agentuse.Device
	if r := e.req(adminTok, "POST", "/api/devices", map[string]string{"name": "x"}, &d, actAsHeader, alice.ID); r.StatusCode != http.StatusCreated || d.UserID != alice.ID {
		t.Fatalf("create device as alice: %d %+v", r.StatusCode, d)
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
	alice, tok := e.user("alice", agentuse.RoleUser)

	var u agentuse.User
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
	_, adminTok := e.user("root", agentuse.RoleAdmin)

	var created struct {
		User     agentuse.User `json:"user"`
		Token    string        `json:"token"`
		LoginURL string        `json:"login_url"`
	}
	if r := e.req(adminTok, "POST", "/api/users", map[string]string{"id": "u_1", "name": "alice"}, &created); r.StatusCode != http.StatusCreated ||
		created.User.Role != agentuse.RoleUser || created.LoginURL != "https://agentuse.example.com/login?token="+created.Token {
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
	var users []agentuse.User
	if e.req(adminTok, "GET", "/api/users", nil, &users); len(users) != 2 {
		t.Fatalf("list: %+v", users)
	}
}

func TestChosenPasswordLoginPreservesSpecialCharactersInCookie(t *testing.T) {
	e := newEnv(t, Config{})
	u, old := e.user("admin", agentuse.RoleAdmin)
	password := "新密码 +%\";$()&!"
	if _, err := e.st.UserService.SetUserToken(context.Background(), u.ID, password); err != nil {
		t.Fatal(err)
	}
	if code := e.code(old, "GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Fatal("old token is valid", code)
	}
	resp := e.req("", "POST", "/api/login", map[string]string{"token": password}, nil)
	loginCookie := cookie(resp)
	if resp.StatusCode != http.StatusOK || loginCookie == nil || loginCookie.Value != url.QueryEscape(password) {
		t.Fatal("custom password login failed", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/me", nil)
	req.AddCookie(loginCookie)
	me, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Fatal("cookie lost password characters", me.StatusCode)
	}
}

func TestHarnessesAPIIsOnlyADeviceReportView(t *testing.T) {
	e := newEnv(t, Config{})
	u, token := e.user("alice", agentuse.RoleUser)
	e.deviceWith(u, "custom-local")
	var hs []agentuse.Harness
	if r := e.req(token, "GET", "/api/harnesses", nil, &hs); r.StatusCode != 200 || len(hs) != 1 || hs[0].Name != "custom-local" {
		t.Fatal(r.StatusCode, hs)
	}
	if code := e.code(token, "POST", "/api/harnesses", map[string]string{"name": "server-config"}); code != http.StatusMethodNotAllowed {
		t.Fatal("server harness registration still enabled", code)
	}
}

// deviceWith 建一台 u 的、已上报并开启 harnesses 的 device。
func (e *env) deviceWith(u agentuse.User, harnesses ...string) agentuse.Device {
	e.t.Helper()
	ctx := context.Background()
	d, _ := e.st.DeviceService.CreateDevice(ctx, u.ID, "laptop")
	d, _, _ = e.st.DeviceService.RegisterDevice(ctx, d.SetupToken, "")
	var rep []deviceservice.ReportedHarness
	for _, h := range harnesses {
		rep = append(rep, deviceservice.ReportedHarness{Name: h})
	}
	e.st.DeviceService.DeviceConnected(ctx, d, rep)
	for _, h := range harnesses {
		e.st.DeviceService.SetDeviceHarnessEnabled(ctx, u, d.ID, h, true)
	}
	return d
}

func TestAgentsAPI(t *testing.T) {
	e := newEnv(t, Config{})
	alice, aliceTok := e.user("alice", agentuse.RoleUser)
	_, bobTok := e.user("bob", agentuse.RoleUser)
	d := e.deviceWith(alice, "claude", "codex")

	var a agentuse.Agent
	if r := e.req(aliceTok, "POST", "/api/agents", map[string]string{"id": "coder", "name": "写代码", "device_id": d.ID, "harness": "claude"}, &a); r.StatusCode != http.StatusCreated || a.ID != "coder" {
		t.Fatalf("create: %d %+v", r.StatusCode, a)
	}
	if c := e.code(aliceTok, "POST", "/api/agents", map[string]string{"name": "x", "device_id": d.ID, "harness": "ghost"}); c != http.StatusBadRequest {
		t.Fatalf("bad runtime: %d", c)
	}
	if c := e.code(aliceTok, "POST", "/api/agents", "["); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	if c := e.code(bobTok, "GET", "/api/agents/coder", nil); c != http.StatusNotFound {
		t.Fatalf("bob get: %d", c)
	}
	base := a.CurrentVersionID
	var v agentuse.AgentVersion
	body := map[string]any{"base_version_id": base, "name": a.Name, "description": "新说明", "device_id": d.ID, "harness": "claude", "skills": []any{}}
	if r := e.req(aliceTok, "POST", "/api/agents/coder/versions", body, &v); r.StatusCode != 201 || v.Version != 2 || v.Description != "新说明" {
		t.Fatalf("publish: %d %+v", r.StatusCode, v)
	}
	if c := e.code(aliceTok, "POST", "/api/agents/coder/versions", body); c != http.StatusConflict {
		t.Fatalf("stale base: %d", c)
	}
	body["base_version_id"], body["harness"] = v.ID, "codex"
	if r := e.req(aliceTok, "POST", "/api/agents/coder/versions", body, &v); r.StatusCode != 201 || v.Version != 3 || v.Harness != "codex" {
		t.Fatalf("runtime: %d %+v", r.StatusCode, v)
	}
	if c := e.code(aliceTok, "POST", "/api/agents/coder/versions", "x"); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	var list []agentuse.Agent
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

func TestDevicesAPI(t *testing.T) {
	e := newEnv(t, Config{})
	_, aliceTok := e.user("alice", agentuse.RoleUser)
	_, bobTok := e.user("bob", agentuse.RoleUser)

	// 不带请求体也能建
	var v deviceView
	if r := e.req(aliceTok, "POST", "/api/devices", nil, &v); r.StatusCode != http.StatusCreated || v.Name != "" ||
		v.SetupCommand != `curl -fsSL "`+e.ts.URL+`/setup.sh" -o setup.sh && AGENTUSE_SERVER="`+e.ts.URL+`" TOKEN="`+v.SetupToken+`" bash setup.sh` {
		t.Fatalf("create: %d %+v", r.StatusCode, v)
	}
	if c := e.code(aliceTok, "POST", "/api/devices", "{"); c != http.StatusBadRequest {
		t.Fatalf("bad json: %d", c)
	}
	var list []deviceView
	if e.req(aliceTok, "GET", "/api/devices", nil, &list); len(list) != 1 || list[0].SetupCommand == "" {
		t.Fatalf("list: %+v", list)
	}

	// 注册后不再有安装命令
	var reg protocol.RegisterResponse
	if r := e.req("", "POST", "/api/client/register", map[string]string{"token": v.SetupToken, "hostname": "host"}, &reg); r.StatusCode != 200 || reg.DeviceID != v.ID {
		t.Fatalf("register: %d %+v", r.StatusCode, reg)
	}
	var after []deviceView
	if e.req(aliceTok, "GET", "/api/devices", nil, &after); after[0].SetupCommand != "" || after[0].SetupToken != "" || after[0].Name != "host" {
		t.Fatalf("after register: %+v", after)
	}
	if c := e.code("", "POST", "/api/client/register", map[string]string{"token": v.SetupToken}); c != http.StatusUnauthorized {
		t.Fatalf("reuse setup token: %d", c)
	}
	if c := e.code("", "POST", "/api/client/register", "x"); c != http.StatusBadRequest {
		t.Fatalf("register bad json: %d", c)
	}
	if c := e.code(reg.Key, "GET", "/api/client/me", nil); c != http.StatusOK {
		t.Fatalf("device me: %d", c)
	}
	if c := e.code("", "GET", "/api/client/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("device me without key: %d", c)
	}

	// 开关只能开上报过的
	if c := e.code(aliceTok, "PUT", "/api/devices/"+v.ID+"/harnesses/claude", map[string]bool{"enabled": true}); c != http.StatusNotFound {
		t.Fatalf("toggle unreported: %d", c)
	}
	if c := e.code(aliceTok, "PUT", "/api/devices/"+v.ID+"/harnesses/claude", "x"); c != http.StatusBadRequest {
		t.Fatalf("toggle bad json: %d", c)
	}
	d, _ := e.st.DeviceService.DeviceByKey(context.Background(), reg.Key)
	e.st.DeviceService.DeviceConnected(context.Background(), d, []deviceservice.ReportedHarness{{Name: "claude"}})
	if r := e.req(aliceTok, "PUT", "/api/devices/"+v.ID+"/harnesses/claude", map[string]bool{"enabled": true}, &v); r.StatusCode != 200 || !v.Harnesses[0].Enabled {
		t.Fatalf("toggle: %d %+v", r.StatusCode, v)
	}

	// 重置：新安装命令，旧密钥失效
	if c := e.code(bobTok, "POST", "/api/devices/"+v.ID+"/reset", nil); c != http.StatusNotFound {
		t.Fatalf("bob reset: %d", c)
	}
	if r := e.req(aliceTok, "POST", "/api/devices/"+v.ID+"/reset", nil, &v); r.StatusCode != 200 || v.SetupCommand == "" || v.Status != agentuse.DevicePending {
		t.Fatalf("reset: %d %+v", r.StatusCode, v)
	}
	if c := e.code(reg.Key, "GET", "/api/client/me", nil); c != http.StatusUnauthorized {
		t.Fatalf("old key after reset: %d", c)
	}

	if c := e.code(bobTok, "DELETE", "/api/devices/"+v.ID, nil); c != http.StatusNotFound {
		t.Fatalf("bob delete: %d", c)
	}
	if c := e.code(aliceTok, "DELETE", "/api/devices/"+v.ID, nil); c != http.StatusNoContent {
		t.Fatalf("delete: %d", c)
	}
}
