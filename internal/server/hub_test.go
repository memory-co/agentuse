package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/store"
)

// fakeDaemon 是一条按 protocol 说话的 WebSocket 连接，扮演 daemon。
type fakeDaemon struct {
	t  *testing.T
	ws *websocket.Conn
}

func (e *env) dial(key string) (*fakeDaemon, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+protocol.WSPath, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + key}},
	})
	if err != nil {
		return nil, resp, err
	}
	fd := &fakeDaemon{t: e.t, ws: ws}
	e.t.Cleanup(func() { ws.CloseNow() })
	return fd, resp, nil
}

func (d *fakeDaemon) send(m protocol.Message) {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, d.ws, m); err != nil {
		d.t.Fatal(err)
	}
}

func (d *fakeDaemon) recv() protocol.Message {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m protocol.Message
	if err := wsjson.Read(ctx, d.ws, &m); err != nil {
		d.t.Fatal(err)
	}
	return m
}

// registeredDaemon 建一台 alice 的 daemon 并注册，返回 daemon 和密钥。
func (e *env) registeredDaemon(u store.User) (store.Daemon, string) {
	e.t.Helper()
	ctx := context.Background()
	d, _ := e.st.CreateDaemon(ctx, u.ID, "laptop")
	d, key, err := e.st.RegisterDaemon(ctx, d.SetupToken, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return d, key
}

func TestHubDispatch(t *testing.T) {
	e := newEnv(t, Config{})
	ctx := context.Background()
	alice, aliceTok := e.user("alice", store.RoleUser)
	d, key := e.registeredDaemon(alice)

	if _, resp, err := e.dial("dk_bad"); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad key: %v", err)
	}

	fd, _, err := e.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	fd.send(protocol.Message{Type: protocol.TypeHello, Harnesses: []protocol.Harness{{Name: "claude", Version: "1"}, {Name: "ghost"}}})
	if w := fd.recv(); w.Type != protocol.TypeWelcome || w.DaemonID != d.ID || len(w.Ignored) != 1 || w.Ignored[0] != "ghost" {
		t.Fatalf("welcome: %+v", w)
	}
	if got, _ := e.st.GetDaemon(ctx, alice, d.ID); got.Status != store.DaemonOnline || len(got.Harnesses) != 1 {
		t.Fatalf("online: %+v", got)
	}

	// 空闲时来了活：建会话后被叫醒派出
	e.code(aliceTok, "PUT", "/api/daemons/"+d.ID+"/harnesses/claude", map[string]bool{"enabled": true})
	e.st.CreateAgent(ctx, alice.ID, "coder", "c", d.ID, "claude", "")
	var created struct {
		Session store.Session `json:"session"`
		Message store.Message `json:"message"`
	}
	fd.send(protocol.Message{Type: protocol.TypeReady})
	e.req(aliceTok, "POST", "/api/sessions", map[string]string{"agent_id": "coder", "prompt": "hi"}, &created)
	m := fd.recv()
	if m.Type != protocol.TypeMessage || m.Message.ID != created.Message.ID || m.Message.SessionID != created.Session.ID ||
		m.Message.Harness != "claude" || m.Message.Resume != "" || string(m.Message.Input) != `{"prompt":"hi"}` {
		t.Fatalf("dispatch: %+v %+v", m, m.Message)
	}

	fd.send(protocol.Message{Type: protocol.TypeHeartbeat, MessageID: m.Message.ID})
	fd.send(protocol.Message{Type: protocol.TypeEvent, MessageID: m.Message.ID, Kind: "progress", Payload: json.RawMessage(`{"text":"p"}`)})
	fd.send(protocol.Message{Type: protocol.TypeEvent, MessageID: m.Message.ID, Kind: "done", Payload: json.RawMessage(`{"ok":true,"session_id":"h-1"}`)})
	waitUntil(t, func() bool {
		ss, _ := e.st.GetSession(ctx, alice, created.Session.ID)
		return ss.Status == store.SessionIdle && ss.HarnessSessionID == "h-1"
	})

	// 回复：ready 之后派出，带上 resume
	e.code(aliceTok, "POST", "/api/sessions/"+created.Session.ID+"/messages", map[string]string{"prompt": "again"})
	fd.send(protocol.Message{Type: protocol.TypeReady})
	if m = fd.recv(); m.Type != protocol.TypeMessage || m.Message.Resume != "h-1" {
		t.Fatalf("reply dispatch: %+v", m.Message)
	}

	// 回报已经不归它的消息 → 收到带 message_id 的 error
	fd.send(protocol.Message{Type: protocol.TypeEvent, MessageID: 999, Kind: "done", Payload: json.RawMessage(`{}`)})
	if er := fd.recv(); er.Type != protocol.TypeError || er.MessageID != 999 {
		t.Fatalf("stale event: %+v", er)
	}
	fd.send(protocol.Message{Type: protocol.TypeHeartbeat, MessageID: 999})
	if er := fd.recv(); er.Type != protocol.TypeError || er.MessageID != 999 {
		t.Fatalf("stale heartbeat: %+v", er)
	}
	fd.send(protocol.Message{Type: "bogus"})
	if er := fd.recv(); er.Type != protocol.TypeError || !strings.Contains(er.Error, "bogus") {
		t.Fatalf("unknown type: %+v", er)
	}

	// 重连:hello 里没说在跑的会话放回 pending,再派一次
	fd2, _, err := e.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	fd2.send(protocol.Message{Type: protocol.TypeHello, Harnesses: []protocol.Harness{{Name: "claude"}}})
	fd2.recv() // welcome
	fd2.send(protocol.Message{Type: protocol.TypeReady})
	if m2 := fd2.recv(); m2.Type != protocol.TypeMessage || m2.Message.ID != m.Message.ID {
		t.Fatalf("redispatch after reconnect: %+v", m2)
	}
	// 旧连接被新连接顶掉
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, _, err := fd.ws.Read(ctx2); err == nil {
		t.Fatalf("old conn should be closed: %v", err)
	}

	// 删除 daemon:连接被断开
	if c := e.code(aliceTok, "DELETE", "/api/daemons/"+d.ID, nil); c != http.StatusNoContent {
		t.Fatalf("delete: %d", c)
	}
	if _, _, err := fd2.ws.Read(ctx2); err == nil {
		t.Fatal("conn should be closed after delete")
	}
}

func TestHubDisconnect(t *testing.T) {
	e := newEnv(t, Config{})
	alice, _ := e.user("alice", store.RoleUser)
	d, key := e.registeredDaemon(alice)
	fd, _, err := e.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	fd.send(protocol.Message{Type: protocol.TypeHello})
	fd.recv()
	fd.ws.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, func() bool {
		got, _ := e.st.GetDaemon(context.Background(), alice, d.ID)
		return got.Status == store.DaemonOffline
	})
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timeout")
}
