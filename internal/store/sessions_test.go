package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestClaimAndLease(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	u, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	d, _ := s.CreateDaemon(ctx, u.ID, "")
	d, _, err = s.RegisterDaemon(ctx, d.SetupToken, "host-a")
	if err != nil || d.Name != "host-a" {
		t.Fatalf("register: %v %+v", err, d)
	}
	// 运行时要是 daemon 上报过的 harness
	if _, err := s.CreateAgent(ctx, u.ID, "", "写代码", d.ID, "claude", ""); err == nil {
		t.Fatal("unreported harness should fail")
	}
	s.DaemonConnected(ctx, d, []ReportedHarness{{Name: "claude", Version: "1.0"}})
	ag, err := s.CreateAgent(ctx, u.ID, "", "写代码", d.ID, "claude", "")
	if err != nil || ag.DaemonID != d.ID {
		t.Fatalf("create agent: %v %+v", err, ag)
	}
	if _, _, err := s.CreateSession(ctx, u, "ag_nope", "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown agent should fail")
	}
	ss, m1, _ := s.CreateSession(ctx, u, ag.ID, "x", json.RawMessage(`{"prompt":"x"}`))
	if ss.AgentID != ag.ID || ss.Harness != "claude" || ss.DaemonID != d.ID || ss.UserID != u.ID {
		t.Fatalf("session from agent: %+v", ss)
	}

	// 本机这次没上报 / 没开启都领不到
	s.DaemonConnected(ctx, d, nil)
	if _, err := s.ClaimSession(ctx, d, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim before report: %v", err)
	}
	ignored, _ := s.DaemonConnected(ctx, d, []ReportedHarness{{Name: "claude", Version: "1.0"}, {Name: "ghost"}})
	if len(ignored) != 1 || ignored[0] != "ghost" {
		t.Fatalf("ignored: %v", ignored)
	}
	if _, err := s.ClaimSession(ctx, d, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim before enable: %v", err)
	}
	if err := s.SetDaemonHarnessEnabled(ctx, u, d.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
	// 重连上报不会丢掉 enabled
	s.DaemonConnected(ctx, d, []ReportedHarness{{Name: "claude", Version: "1.1"}})
	got, err := s.ClaimSession(ctx, d, 60)
	if err != nil || got.Session.ID != ss.ID || got.Session.Status != SessionRunning || got.Message.ID != m1.ID {
		t.Fatalf("claim: %v %+v", err, got)
	}
	// 还没回完不能再发
	if _, _, err := s.AddMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"y"}`)); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("busy: %v", err)
	}

	// 租约过期 → 回到 pending
	now = now.Add(61 * time.Second)
	users, _ := s.RequeueExpired(ctx)
	if len(users) != 1 || users[0] != u.ID {
		t.Fatalf("requeue: %v", users)
	}
	if err := s.AddEvent(ctx, d.ID, m1.ID, EventProgress, nil, 60); !errors.Is(err, ErrStaleMessage) {
		t.Fatalf("stale progress: %v", err)
	}
	got, _ = s.ClaimSession(ctx, d, 60)
	if err := s.AddEvent(ctx, d.ID, got.Message.ID, EventDone, json.RawMessage(`{"ok":true,"session_id":"h-1"}`), 60); err != nil {
		t.Fatal(err)
	}
	ss, _ = s.GetSession(ctx, u, ss.ID)
	if ss.Status != SessionIdle || ss.HarnessSessionID != "h-1" || ss.DaemonID != d.ID {
		t.Fatalf("done: %+v", ss)
	}
	msgs, _ := s.Messages(ctx, ss.ID)
	if len(msgs) != 2 || msgs[1].Role != RoleAssistantMsg || string(msgs[1].Content) != `{"ok":true,"session_id":"h-1"}` {
		t.Fatalf("messages: %+v", msgs)
	}

	// 回复：只派给会话的 daemon,带上 harness 会话 id
	d2, _ := s.CreateDaemon(ctx, u.ID, "")
	d2, _, _ = s.RegisterDaemon(ctx, d2.SetupToken, "host-b")
	s.DaemonConnected(ctx, d2, []ReportedHarness{{Name: "claude"}})
	s.SetDaemonHarnessEnabled(ctx, u, d2.ID, "claude", true)
	_, m3, err := s.AddMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimSession(ctx, d2, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other daemon should not get bound session: %v", err)
	}
	got, err = s.ClaimSession(ctx, d, 60)
	if err != nil || got.Message.ID != m3.ID || got.Session.HarnessSessionID != "h-1" {
		t.Fatalf("claim reply: %v %+v", err, got)
	}
	// 重连时没在跑 → 收回
	if users, _ := s.RequeueDaemonSessions(ctx, d.ID, []int64{m3.ID}); len(users) != 0 {
		t.Fatalf("kept should stay running: %v", users)
	}
	if users, _ := s.RequeueDaemonSessions(ctx, d.ID, nil); len(users) != 1 {
		t.Fatalf("requeue on reconnect: %v", users)
	}

	// 删除 daemon → 没回完的记失败、会话不能再发
	if err := s.DeleteDaemon(ctx, u, d.ID); err != nil {
		t.Fatal(err)
	}
	ss, _ = s.GetSession(ctx, u, ss.ID)
	msgs, _ = s.Messages(ctx, ss.ID)
	if ss.Status != SessionIdle || ss.DaemonID != "" || len(msgs) != 4 || msgs[3].Role != RoleAssistantMsg {
		t.Fatalf("after delete daemon: %+v %+v", ss, msgs)
	}
	if _, _, err := s.AddMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"z"}`)); !errors.Is(err, ErrSessionOrphaned) {
		t.Fatalf("orphaned: %v", err)
	}
	if err := s.DeleteSession(ctx, u, ss.ID); err != nil {
		t.Fatal(err)
	}

	// daemon 删了,agent 没有运行时，开不了会话;切换到 d2 后能开，只派给 d2
	if ag, _ = s.GetAgent(ctx, u, ag.ID); ag.DaemonID != "" {
		t.Fatalf("agent after daemon delete: %+v", ag)
	}
	if _, _, err := s.CreateSession(ctx, u, ag.ID, "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("agent without runtime should fail")
	}
	d2id, claude := d2.ID, "claude"
	if ag, err = s.UpdateAgent(ctx, u, ag.ID, AgentPatch{DaemonID: &d2id, Harness: &claude}); err != nil || ag.DaemonID != d2.ID {
		t.Fatalf("switch runtime: %v %+v", err, ag)
	}
	s4, _, _ := s.CreateSession(ctx, u, ag.ID, "", json.RawMessage(`{}`))
	if got, err := s.ClaimSession(ctx, d2, 60); err != nil || got.Session.ID != s4.ID {
		t.Fatalf("claim on new runtime: %v %+v", err, got)
	}

	// 本机不再上报 claude → 不可用，不再派
	s.DaemonConnected(ctx, d2, nil)
	s.CreateSession(ctx, u, ag.ID, "", json.RawMessage(`{}`))
	if _, err := s.ClaimSession(ctx, d2, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim unavailable: %v", err)
	}
}

func TestSessionValidation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	d := registered(t, s, alice, "laptop", "claude")
	s.SetDaemonHarnessEnabled(ctx, alice, d.ID, "claude", true)
	ag, _ := s.CreateAgent(ctx, alice.ID, "", "a", d.ID, "claude", "")

	if _, _, err := s.CreateSession(ctx, alice, ag.ID, "", json.RawMessage(`not json`)); err == nil {
		t.Fatal("invalid content should fail")
	}
	ss, m, err := s.CreateSession(ctx, alice, ag.ID, "标题", json.RawMessage(`{"prompt":"x"}`))
	if err != nil || ss.Title != "标题" || m.Role != RoleUserMsg {
		t.Fatalf("create: %v %+v %+v", err, ss, m)
	}
	if _, _, err := s.AddMessage(ctx, alice, ss.ID, json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid reply should fail")
	}
	if _, _, err := s.AddMessage(ctx, alice, "ss_nope", json.RawMessage(`{}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}

	got, _ := s.ClaimSession(ctx, d, 60)
	if err := s.DeleteSession(ctx, alice, ss.ID); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("delete running: %v", err)
	}
	for _, c := range []struct {
		kind    string
		payload string
	}{{"weird", `{}`}, {EventProgress, `{`}} {
		if err := s.AddEvent(ctx, d.ID, got.Message.ID, c.kind, json.RawMessage(c.payload), 60); err == nil || errors.Is(err, ErrStaleMessage) {
			t.Fatalf("AddEvent(%s, %s): %v", c.kind, c.payload, err)
		}
	}
	// 别的 daemon 回报不算数
	other := registered(t, s, alice, "other", "claude")
	if err := s.AddEvent(ctx, other.ID, got.Message.ID, EventDone, nil, 60); !errors.Is(err, ErrStaleMessage) {
		t.Fatalf("other daemon: %v", err)
	}
	if err := s.RenewLease(ctx, other.ID, got.Message.ID, 60); !errors.Is(err, ErrStaleMessage) {
		t.Fatalf("other daemon renew: %v", err)
	}
	if err := s.RenewLease(ctx, d.ID, got.Message.ID, 600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEvent(ctx, d.ID, got.Message.ID, EventProgress, json.RawMessage(`{"text":"…"}`), 60); err != nil {
		t.Fatal(err)
	}
	// failed 也回到 idle;没带 session_id 时不覆盖
	if err := s.AddEvent(ctx, d.ID, got.Message.ID, EventFailed, json.RawMessage(`{"ok":false,"error":"x"}`), 60); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEvent(ctx, d.ID, got.Message.ID, EventDone, nil, 60); !errors.Is(err, ErrStaleMessage) {
		t.Fatalf("second result for the same message: %v", err)
	}
	evs, _ := s.SessionEvents(ctx, ss.ID)
	if len(evs) != 2 || evs[0].Kind != EventProgress || string(evs[0].Payload) != `{"text":"…"}` || evs[1].Kind != EventFailed || evs[1].DaemonID != d.ID {
		t.Fatalf("events: %+v", evs)
	}
	if got, _ := s.GetSession(ctx, alice, ss.ID); got.Status != SessionIdle || got.HarnessSessionID != "" || got.LeaseUntil != nil {
		t.Fatalf("after failed: %+v", got)
	}
	if err := s.DeleteSession(ctx, alice, ss.ID); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := s.Messages(ctx, ss.ID); len(msgs) != 0 {
		t.Fatalf("messages should be deleted with the session: %+v", msgs)
	}
}

func TestListSessions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	root, _, _ := s.CreateUser(ctx, "", "root", RoleAdmin)
	ag, _ := s.CreateAgent(ctx, alice.ID, "", "a", registered(t, s, alice, "laptop", "claude").ID, "claude", "")
	bg, _ := s.CreateAgent(ctx, bob.ID, "", "b", registered(t, s, bob, "pc", "claude").ID, "claude", "")

	var ids []string
	for i := 0; i < 3; i++ {
		ss, _, _ := s.CreateSession(ctx, alice, ag.ID, "", json.RawMessage(`{}`))
		ids = append(ids, ss.ID)
	}
	s.CreateSession(ctx, bob, bg.ID, "", json.RawMessage(`{}`))

	// 最近更新的在前,limit 生效
	l, _ := s.ListSessions(ctx, alice, 2)
	if len(l) != 2 || l[0].ID != ids[2] || l[1].ID != ids[1] {
		t.Fatalf("alice: %+v", l)
	}
	if l, _ := s.ListSessions(ctx, bob, 10); len(l) != 1 {
		t.Fatalf("bob: %+v", l)
	}
	if l, _ := s.ListSessions(ctx, root, 10); len(l) != 4 {
		t.Fatalf("admin: %+v", l)
	}
	if _, err := s.GetSession(ctx, root, ids[0]); err != nil {
		t.Fatalf("admin get: %v", err)
	}
}
