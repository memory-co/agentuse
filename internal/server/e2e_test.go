package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/server"
	"github.com/memory-co/agentuse/internal/store"
)

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c client) do(method, path string, body, out any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// 一个按约定从 stdin 读输入、往 stdout 写 JSON 行的假 harness,会话 id 固定为 h-1。
const harness = `#!/bin/sh
read input
echo '{"type":"progress","text":"working"}'
echo "not json line"
esc=$(printf '%s' "$input" | sed 's/"/\\"/g')
echo "{\"type\":\"result\",\"ok\":true,\"output\":\"echo: $esc\",\"session_id\":\"h-1\"}"
`

func TestEndToEnd(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := server.New(st, server.Config{Logger: log.New(io.Discard, "", 0)})
	go srv.Run(ctx)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	_, aliceTok, _ := st.CreateUser(ctx, "", "alice", store.RoleUser)
	_, bobTok, _ := st.CreateUser(ctx, "", "bob", store.RoleUser)
	ca := client{t, ts.URL, aliceTok}
	cb := client{t, ts.URL, bobTok}

	// 自定义 harness
	if code := ca.do("POST", "/api/harnesses", map[string]string{"name": "echo-harness"}, nil); code != http.StatusCreated {
		t.Fatalf("create harness: %d", code)
	}
	// alice 和 bob 各建一个用它的 agent
	var aliceAgent, bobAgent store.Agent
	if code := ca.do("POST", "/api/agents", map[string]string{"id": "alice-echo", "name": "回声", "harness": "echo-harness"}, &aliceAgent); code != http.StatusCreated {
		t.Fatalf("create agent: %d", code)
	}
	cb.do("POST", "/api/agents", map[string]string{"name": "回声", "harness": "echo-harness"}, &bobAgent)

	// 页面上添加 daemon,拿到安装命令
	var dv struct {
		store.Daemon
		SetupCommand string `json:"setup_command"`
	}
	ca.do("POST", "/api/daemons", map[string]string{"name": "laptop"}, &dv)
	if dv.Status != store.DaemonPending || !strings.Contains(dv.SetupCommand, `TOKEN="`+dv.SetupToken+`" bash setup.sh`) ||
		!strings.Contains(dv.SetupCommand, ts.URL+"/setup.sh") {
		t.Fatalf("bad daemon view: %+v", dv)
	}

	// setup.sh 带上服务地址
	resp, err := http.Get(ts.URL + "/setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(script), `SERVER="${AGENTUSE_SERVER:-`+ts.URL+`}"`) {
		t.Fatalf("setup.sh 没有替换服务地址")
	}

	// 注册:令牌只能用一次
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "daemon.json")
	hpath := filepath.Join(dir, "harness.sh")
	os.WriteFile(hpath, []byte(harness), 0o755)
	if err := daemon.SaveConfig(cfgPath, daemon.Config{
		Workdir: filepath.Join(dir, "work"),
		Harnesses: map[string]daemon.HarnessConfig{
			"echo-harness": {Command: hpath},
			"claude":       {Disabled: true},
			"codex":        {Disabled: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := daemon.Register(ctx, ts.URL, dv.SetupToken, cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.Register(ctx, ts.URL, dv.SetupToken, filepath.Join(dir, "x.json")); err == nil {
		t.Fatal("安装令牌应该只能用一次")
	}

	// 先开会话，此时 harness 还没开启，不会派
	var created struct {
		Session store.Session `json:"session"`
		Message store.Message `json:"message"`
	}
	ca.do("POST", "/api/sessions", map[string]string{"agent_id": aliceAgent.ID, "prompt": "hi"}, &created)
	ss := created.Session
	if ss.Status != store.SessionPending || ss.AgentID != aliceAgent.ID || ss.Harness != "echo-harness" || ss.Title != "hi" || created.Message.Role != store.RoleUserMsg {
		t.Fatalf("session: %+v", created)
	}

	d, err := daemon.New(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	go d.Run(ctx)

	// 等 daemon 上线、上报 harness
	var got struct {
		store.Daemon
	}
	waitFor(t, func() bool {
		ca.do("GET", "/api/daemons/"+dv.ID, nil, nil)
		var list []store.Daemon
		ca.do("GET", "/api/daemons", nil, &list)
		if len(list) == 1 && list[0].Status == store.DaemonOnline && len(list[0].Harnesses) == 1 {
			got.Daemon = list[0]
			return true
		}
		return false
	})
	if a := got.Harnesses[0]; a.Harness != "echo-harness" || !a.Available || a.Enabled {
		t.Fatalf("daemon harness: %+v", a)
	}

	// bob 看不到 alice 的 daemon,也开不了它的 harness
	var bobList []store.Daemon
	cb.do("GET", "/api/daemons", nil, &bobList)
	if len(bobList) != 0 {
		t.Fatalf("bob sees %d daemons", len(bobList))
	}
	if code := cb.do("PUT", "/api/daemons/"+dv.ID+"/harnesses/echo-harness", map[string]bool{"enabled": true}, nil); code != http.StatusNotFound {
		t.Fatalf("bob toggle: %d", code)
	}
	// bob 的同名 harness 会话不能派给 alice 的 daemon
	var bobCreated struct {
		Session store.Session `json:"session"`
	}
	cb.do("POST", "/api/sessions", map[string]string{"agent_id": bobAgent.ID, "prompt": "bob"}, &bobCreated)
	bobSession := bobCreated.Session

	// 开启 harness → 排队的会话被派出，harness 回复
	if code := ca.do("PUT", "/api/daemons/"+dv.ID+"/harnesses/echo-harness", map[string]bool{"enabled": true}, nil); code != http.StatusOK {
		t.Fatalf("toggle: %d", code)
	}
	var detail struct {
		Session  store.Session        `json:"session"`
		Messages []store.Message      `json:"messages"`
		Events   []store.SessionEvent `json:"events"`
	}
	waitFor(t, func() bool {
		ca.do("GET", "/api/sessions/"+ss.ID, nil, &detail)
		return detail.Session.Status == store.SessionIdle
	})
	var res daemon.Result
	json.Unmarshal(detail.Messages[1].Content, &res)
	if !res.OK || !strings.Contains(res.Output, `"prompt":"hi"`) || !strings.Contains(res.Output, `"session_id":"`+ss.ID+`"`) ||
		detail.Session.DaemonID != dv.ID || detail.Session.HarnessSessionID != "h-1" {
		t.Fatalf("result: %+v %s", detail.Session, detail.Messages[1].Content)
	}
	if len(detail.Events) != 3 || detail.Events[0].Kind != "progress" || detail.Events[2].Kind != "done" {
		t.Fatalf("events: %+v", detail.Events)
	}

	// 回复：同一个会话，harness 拿到上次的会话 id 续接
	if code := ca.do("POST", "/api/sessions/"+ss.ID+"/messages", map[string]string{"prompt": "again"}, nil); code != http.StatusCreated {
		t.Fatalf("reply: %d", code)
	}
	if code := ca.do("POST", "/api/sessions/"+ss.ID+"/messages", map[string]string{"prompt": "too soon"}, nil); code != http.StatusConflict {
		t.Fatalf("reply while busy should be 409: %d", code)
	}
	waitFor(t, func() bool {
		ca.do("GET", "/api/sessions/"+ss.ID, nil, &detail)
		return detail.Session.Status == store.SessionIdle && len(detail.Messages) == 4
	})
	json.Unmarshal(detail.Messages[3].Content, &res)
	if !res.OK || !strings.Contains(res.Output, `"prompt":"again"`) || !strings.Contains(res.Output, `"resume":"h-1"`) {
		t.Fatalf("reply result: %s", detail.Messages[3].Content)
	}

	if code := cb.do("GET", "/api/sessions/"+bobSession.ID, nil, &detail); code != http.StatusOK || detail.Session.Status != store.SessionPending {
		t.Fatalf("bob session should stay pending: %d %+v", code, detail.Session)
	}
	if code := cb.do("GET", "/api/sessions/"+ss.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("bob should not see alice session: %d", code)
	}

	// bob 用 alice 的 agent 开会话:跑在 alice 的 daemon 上,bob 看得到结果
	var at struct {
		Session store.Session `json:"session"`
	}
	if code := cb.do("POST", "/api/sessions", map[string]string{"agent_id": aliceAgent.ID, "prompt": "from bob"}, &at); code != http.StatusCreated ||
		at.Session.UserID != aliceAgent.UserID || at.Session.CreatedBy == aliceAgent.UserID {
		t.Fatalf("bob uses alice's agent: %d %+v", code, at.Session)
	}
	waitFor(t, func() bool {
		cb.do("GET", "/api/sessions/"+at.Session.ID, nil, &detail)
		return detail.Session.Status == store.SessionIdle
	})
	if json.Unmarshal(detail.Messages[1].Content, &res); !res.OK || detail.Session.DaemonID != dv.ID || !strings.Contains(res.Output, "from bob") {
		t.Fatalf("bob's session on alice's daemon: %+v %s", detail.Session, detail.Messages[1].Content)
	}

	// daemon 用密钥自检
	req, _ := http.NewRequest("GET", ts.URL+"/api/daemon/me", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("daemon me: %v %v", err, resp)
	}
	if code := ca.do("POST", "/api/users/"+bobSession.UserID+"/token", nil, nil); code != http.StatusForbidden {
		t.Fatalf("普通用户不能重置令牌: %d", code)
	}

	// 删除 daemon 后密钥失效
	if code := ca.do("DELETE", "/api/daemons/"+dv.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if _, err := st.DaemonByKey(ctx, cfg.Key); err == nil {
		t.Fatal("key should be invalid after delete")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout")
}

func TestActAsUser(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ts := httptest.NewServer(server.New(st, server.Config{Logger: log.New(io.Discard, "", 0)}))
	defer ts.Close()
	_, adminTok, _ := st.CreateUser(ctx, "", "root", store.RoleAdmin)
	_, aliceTok, _ := st.CreateUser(ctx, "alice", "alice", store.RoleUser)

	as := func(tok, user, method, path string, body, out any) int {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, r)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Agentuse-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}

	// 不存在的用户自动建
	var me store.User
	if code := as(adminTok, "u_10086", "GET", "/api/me", nil, &me); code != 200 || me.ID != "u_10086" || me.Role != store.RoleUser {
		t.Fatalf("act as new user: %d %+v", code, me)
	}
	// 代为 alice 建的 agent 挂在 alice 下
	var a store.Agent
	if code := as(adminTok, "alice", "POST", "/api/agents", map[string]string{"name": "x", "harness": "claude"}, &a); code != http.StatusCreated || a.UserID != "alice" {
		t.Fatalf("create agent as alice: %d %+v", code, a)
	}
	// 代为用户时就是普通用户权限
	if code := as(adminTok, "alice", "GET", "/api/users", nil, nil); code != http.StatusForbidden {
		t.Fatalf("admin api as user: %d", code)
	}
	// 普通用户令牌不能带
	if code := as(aliceTok, "u_10086", "GET", "/api/me", nil, nil); code != http.StatusForbidden {
		t.Fatalf("user token with header: %d", code)
	}
}
