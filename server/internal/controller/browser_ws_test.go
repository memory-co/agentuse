package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestBrowserNotificationsAuthenticationAndOrigin(t *testing.T) {
	e := newEnv(t, Config{})
	_, token := e.user("alice", service.RoleUser)
	bob, _ := e.user("bob", service.RoleUser)
	tests := []struct {
		token, path, origin string
		status              int
	}{
		{"", "", "", 401},
		{"ut_invalid", "", "", 401},
		{token, "?as=" + bob.ID, "", 403},
		{token, "", "https://untrusted.example", 403},
		{"", "?token=" + token, "", 401},
	}
	for _, tt := range tests {
		h := http.Header{}
		if tt.token != "" {
			h.Set("Cookie", cookieName+"="+tt.token)
		}
		if tt.origin != "" {
			h.Set("Origin", tt.origin)
		}
		c, r, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/api/browser/ws"+tt.path, &websocket.DialOptions{HTTPHeader: h})
		if c != nil {
			c.CloseNow()
		}
		if err == nil || r == nil || r.StatusCode != tt.status {
			t.Fatalf("handshake %q: %v %+v", tt.path, err, r)
		}
	}
}

func TestBrowserNotificationsScopeCoalescingAndReconnect(t *testing.T) {
	e := newEnv(t, Config{})
	alice, aliceToken := e.user("alice", service.RoleUser)
	bob, bobToken := e.user("bob", service.RoleUser)
	_, adminToken := e.user("admin", service.RoleAdmin)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read := func(c *websocket.Conn) browserNotice {
		t.Helper()
		var n browserNotice
		if err := wsjson.Read(ctx, c, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	dial := func(token, as string) *websocket.Conn {
		t.Helper()
		path := "/api/browser/ws"
		if as != "" {
			path += "?as=" + url.QueryEscape(as)
		}
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+path, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookieName + "=" + token}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.CloseNow() })
		if n := read(c); n.Type != "ready" {
			t.Fatal(n)
		}
		return c
	}
	a := dial(aliceToken, "")
	b := dial(bobToken, "")
	admin := dial(adminToken, "")
	acting := dial(adminToken, alice.ID)
	bConversation, _, err := e.st.CreateConversation(ctx, bob, "", "Bob private", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	aConversation, _, err := e.st.CreateConversation(ctx, alice, "", "Alice private", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err = e.st.SendMessage(ctx, alice, aConversation.ID, "secret message body", nil, nil, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []*websocket.Conn{a, acting} {
		n := read(c)
		if n.Type != "changed" || len(n.Changes) != 1 || n.Changes[0].ID != aConversation.ID {
			t.Fatal(n)
		}
		raw, _ := json.Marshal(n)
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "owners") {
			t.Fatal("content leaked", string(raw))
		}
		detail, e2 := e.st.ReadConversation(ctx, alice, n.Changes[0].ID)
		if e2 != nil || len(detail.Messages) != 20 {
			t.Fatal("notification preceded commit", detail, e2)
		}
	}
	if n := read(b); len(n.Changes) != 1 || n.Changes[0].ID != bConversation.ID {
		t.Fatal(n)
	}
	if n := read(admin); len(n.Changes) != 2 {
		t.Fatal(n)
	}
	a.CloseNow()
	// No replay ledger: reconnect receives ready and fetches the current snapshot.
	_, err = e.st.SendMessage(ctx, alice, aConversation.ID, "while disconnected", nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	a = dial(aliceToken, "")
	detail, err := e.st.ReadConversation(ctx, alice, aConversation.ID)
	if err != nil || len(detail.Messages) != 21 {
		t.Fatal(detail, err)
	}
	if _, _, err = e.st.ResetUserToken(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.SendMessage(ctx, alice, aConversation.ID, "after revocation", nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	var n browserNotice
	if err = wsjson.Read(ctx, a, &n); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("revoked connection remained authorized: %+v %v", n, err)
	}
}

func TestBrowserNotificationsIncludeCommittedClientResults(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reply service.Reply
	if r := f.e.req(f.token, "POST", "/api/conversations/"+f.conversation.ID+"/messages", map[string]any{"prompt": "input", "to_agent_ids": []string{f.agent}}, &reply); r.StatusCode != 201 {
		t.Fatal(r.StatusCode)
	}
	p, err := f.e.st.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.e.ts.URL, "http")+"/api/browser/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookieName + "=" + f.token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var n browserNotice
	if err = wsjson.Read(ctx, c, &n); err != nil || n.Type != "ready" {
		t.Fatal(n, err)
	}
	run := "550e8400-e29b-41d4-a716-446655440000"
	for i, event := range []map[string]any{
		{"type": "accepted", "run_id": run, "input_id": "input", "text": "input"},
		{"type": "result", "run_id": run, "input_ids": []string{"input"}, "status": "completed", "output": "# Done\n\n**Result**"},
	} {
		raw, _ := json.Marshal(event)
		if err = f.e.st.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{ConversationID: f.conversation.ID, SessionID: "native", AgentID: f.agent, AgentVersionID: p.AgentVersionID, Revision: "reported", MessageID: p.MessageID, Seq: int64(i + 1), Event: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err = wsjson.Read(ctx, c, &n); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, change := range n.Changes {
		if change.ID != f.conversation.ID {
			t.Fatal(change)
		}
		seen[change.Resource] = true
	}
	if !seen["conversations"] || !seen["attachments"] {
		t.Fatal(n)
	}
	var detail service.ConversationDetail
	if r := f.e.req(f.token, "GET", "/api/conversations/"+f.conversation.ID, nil, &detail); r.StatusCode != 200 || len(detail.Runs) != 1 || detail.Runs[0].Status != "completed" || detail.Runs[0].Output != "# Done\n\n**Result**" {
		t.Fatal("committed result missing", r.StatusCode, detail)
	}
}
