package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	if _, _, err := s.CreateSession(ctx, u.ID, "nope", "", "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown harness should fail")
	}
	ss, m1, _ := s.CreateSession(ctx, u.ID, "claude", "", "x", json.RawMessage(`{"prompt":"x"}`))

	// 没上报 / 没开启都领不到
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

	// 回复：只派给绑定的 daemon,带上 harness 会话 id
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

	// 本机不再上报 claude → 不可用，不再派
	s.DaemonConnected(ctx, d2, nil)
	s.CreateSession(ctx, u.ID, "claude", "", "", json.RawMessage(`{}`))
	if _, err := s.ClaimSession(ctx, d2, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim unavailable: %v", err)
	}
}

func TestCreateUserID(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	u, _, err := s.CreateUser(ctx, "u_10086", "alice", RoleUser)
	if err != nil || u.ID != "u_10086" {
		t.Fatalf("custom id: %v %+v", err, u)
	}
	if _, _, err := s.CreateUser(ctx, "u_10086", "bob", RoleUser); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("duplicate id: %v", err)
	}
	if _, _, err := s.CreateUser(ctx, "u_2", "alice", RoleUser); err == nil || !strings.Contains(err.Error(), "用户名") {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, _, err := s.CreateUser(ctx, "has space", "carol", RoleUser); err == nil {
		t.Fatal("invalid id should fail")
	}
	g, _, err := s.CreateUser(ctx, "", "dave", RoleUser)
	if err != nil || len(g.ID) != 36 {
		t.Fatalf("generated id: %v %+v", err, g)
	}
}
