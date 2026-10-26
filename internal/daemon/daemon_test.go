package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/memory-co/agentuse/internal/protocol"
)

func TestNew(t *testing.T) {
	if _, err := New(Config{Server: "http://x"}, nil); err == nil {
		t.Fatal("没注册应该报错")
	}
	t.Setenv("HOME", "/home/x")
	d, err := New(Config{Server: "http://x", Key: "dk"}, nil)
	if err != nil || d.workdir != "/home/x/.agentuse/work" {
		t.Fatalf("workdir: %v %q", err, d.workdir)
	}
}

func TestWSURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://a.com":    "wss://a.com" + protocol.WSPath,
		"http://localhost": "ws://localhost" + protocol.WSPath,
		"ws://raw":         "ws://raw" + protocol.WSPath,
	} {
		if got := wsURL(in); got != want {
			t.Errorf("wsURL(%q) = %q", in, got)
		}
	}
}

func TestSessionDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, _ := New(Config{Server: "http://x", Key: "dk"}, log.New(io.Discard, "", 0))
	dir, err := d.sessionDir(protocol.Dispatch{SessionID: "ss_1"})
	if st, _ := os.Stat(dir); err != nil || dir != filepath.Join(home, ".agentuse", "work", "ss_1") || !st.IsDir() {
		t.Fatalf("session dir: %v %q", err, dir)
	}
	for _, id := range []string{"", "..", "../x", "a/b"} {
		if _, err := d.sessionDir(protocol.Dispatch{SessionID: id}); err == nil {
			t.Fatalf("会话 id %q 应该被拒绝", id)
		}
	}
}

// 断线时：进度丢掉，结果留到重连后补发。
func TestSendOffline(t *testing.T) {
	d, _ := New(Config{Server: "http://x", Key: "dk"}, nil)
	for _, m := range []protocol.Message{
		{Type: protocol.TypeEvent, Kind: "progress", MessageID: 1},
		{Type: protocol.TypeEvent, Kind: "done", MessageID: 1},
		{Type: protocol.TypeReady},
	} {
		if err := d.send(m); !errors.Is(err, errOffline) {
			t.Fatalf("offline send: %v", err)
		}
	}
	if len(d.outbox) != 1 || d.outbox[0].Kind != "done" {
		t.Fatalf("outbox: %+v", d.outbox)
	}
	var sent []protocol.Message
	d.conn = func(m protocol.Message) error { sent = append(sent, m); return nil }
	d.send(protocol.Message{Type: protocol.TypeReady})
	if len(sent) != 1 || len(d.outbox) != 1 {
		t.Fatalf("online send: %+v", sent)
	}
}

// fakeServer 扮演服务端的 WebSocket:把收到的消息放进 recv,往 send 里写的发给 daemon。
type fakeServer struct {
	*httptest.Server
	recv chan protocol.Message
	send chan protocol.Message
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{recv: make(chan protocol.Message, 100), send: make(chan protocol.Message, 100)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != protocol.WSPath || r.Header.Get("Authorization") != "Bearer dk_ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case m := <-f.send:
					wsjson.Write(ctx, ws, m)
				}
			}
		}()
		for {
			var m protocol.Message
			if err := wsjson.Read(ctx, ws, &m); err != nil {
				return
			}
			f.recv <- m
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) next(t *testing.T, typ string) protocol.Message {
	t.Helper()
	for {
		select {
		case m := <-f.recv:
			if m.Type == typ {
				return m
			}
			if m.Type != protocol.TypeHeartbeat {
				t.Fatalf("want %s, got %+v", typ, m)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for %s", typ)
		}
	}
}

func TestRun(t *testing.T) {
	f := newFakeServer(t)
	mine := fakeBin(t, t.TempDir(), "mine", `read in; echo '{"type":"progress","text":"p"}'; echo '{"type":"result","ok":true,"output":"done","session_id":"h-1"}'`)
	t.Setenv("HOME", t.TempDir())
	d, _ := New(Config{Server: f.URL, Key: "dk_ok", Harnesses: map[string]HarnessConfig{
		"mine": {Command: mine}, "claude": {Disabled: true}, "codex": {Disabled: true},
	}}, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- d.Run(ctx) }()

	hello := f.next(t, protocol.TypeHello)
	if len(hello.Harnesses) != 1 || hello.Harnesses[0].Name != "mine" || hello.Version != Version {
		t.Fatalf("hello: %+v", hello)
	}
	f.send <- protocol.Message{Type: protocol.TypeWelcome, DaemonID: "dm_1", Ignored: []string{"x"}}
	f.next(t, protocol.TypeReady)

	f.send <- protocol.Message{Type: protocol.TypeMessage, Message: &protocol.Dispatch{ID: 5, SessionID: "ss_1", Harness: "mine", Input: json.RawMessage(`{"prompt":"hi"}`)}}
	if ev := f.next(t, protocol.TypeEvent); ev.Kind != "progress" || ev.MessageID != 5 || string(ev.Payload) != `{"type":"progress","text":"p"}` {
		t.Fatalf("progress: %+v", ev)
	}
	var res Result
	if ev := f.next(t, protocol.TypeEvent); ev.Kind != "done" || json.Unmarshal(ev.Payload, &res) != nil || res.Output != "done" || res.SessionID != "h-1" {
		t.Fatalf("done: %+v", ev)
	}
	f.next(t, protocol.TypeReady)

	// 本机没有的 harness:回报失败
	f.send <- protocol.Message{Type: protocol.TypeMessage, Message: &protocol.Dispatch{ID: 6, SessionID: "ss_2", Harness: "ghost", Input: json.RawMessage(`{}`)}}
	if ev := f.next(t, protocol.TypeEvent); ev.Kind != "failed" || json.Unmarshal(ev.Payload, &res) != nil || res.Error != "本机没有 harness ghost" {
		t.Fatalf("unknown harness: %+v %+v", ev, res)
	}
	f.next(t, protocol.TypeReady)

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run after cancel: %v", err)
	}
}

// 服务说消息不归本机了：停掉正在跑的 harness,不回报结果。
func TestRunStaleMessage(t *testing.T) {
	f := newFakeServer(t)
	slow := fakeBin(t, t.TempDir(), "slow", `read in; echo '{"type":"progress"}'; exec sleep 10`)
	t.Setenv("HOME", t.TempDir())
	d, _ := New(Config{Server: f.URL, Key: "dk_ok", Harnesses: map[string]HarnessConfig{
		"slow": {Command: slow}, "claude": {Disabled: true}, "codex": {Disabled: true},
	}}, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	f.next(t, protocol.TypeHello)
	f.send <- protocol.Message{Type: protocol.TypeWelcome}
	f.next(t, protocol.TypeReady)
	f.send <- protocol.Message{Type: protocol.TypeMessage, Message: &protocol.Dispatch{ID: 9, SessionID: "ss_1", Harness: "slow", Input: json.RawMessage(`{}`)}}
	f.next(t, protocol.TypeEvent)
	start := time.Now()
	f.send <- protocol.Message{Type: protocol.TypeError, MessageID: 9, Error: "stale"}
	f.next(t, protocol.TypeReady) // 没有 done / failed,直接 ready
	if time.Since(start) > 5*time.Second {
		t.Fatal("harness should be killed")
	}
}

func TestRunUnauthorized(t *testing.T) {
	f := newFakeServer(t)
	d, _ := New(Config{Server: f.URL, Key: "dk_bad"}, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Run(ctx); !errors.Is(err, errUnauthorized) {
		t.Fatalf("want errUnauthorized, got %v", err)
	}
}
