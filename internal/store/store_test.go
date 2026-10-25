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
	// 运行时要是 daemon 上报过的 harness
	if _, err := s.CreateAgent(ctx, u.ID, "", "写代码", d.ID, "claude", ""); err == nil {
		t.Fatal("unreported harness should fail")
	}
	s.DaemonConnected(ctx, d, []ReportedHarness{{Name: "claude", Version: "1.0"}})
	ag, err := s.CreateAgent(ctx, u.ID, "", "写代码", d.ID, "claude", "")
	if err != nil || ag.DaemonID != d.ID {
		t.Fatalf("create agent: %v %+v", err, ag)
	}
	if _, _, err := s.CreateSession(ctx, u, "ag_nope", "", "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown agent should fail")
	}
	ss, m1, _ := s.CreateSession(ctx, u, ag.ID, "", "x", json.RawMessage(`{"prompt":"x"}`))
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
	if _, _, err := s.CreateSession(ctx, u, ag.ID, "", "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("agent without runtime should fail")
	}
	d2id, claude := d2.ID, "claude"
	if ag, err = s.UpdateAgent(ctx, u, ag.ID, AgentPatch{DaemonID: &d2id, Harness: &claude}); err != nil || ag.DaemonID != d2.ID {
		t.Fatalf("switch runtime: %v %+v", err, ag)
	}
	s4, _, _ := s.CreateSession(ctx, u, ag.ID, "", "", json.RawMessage(`{}`))
	if got, err := s.ClaimSession(ctx, d2, 60); err != nil || got.Session.ID != s4.ID {
		t.Fatalf("claim on new runtime: %v %+v", err, got)
	}

	// 本机不再上报 claude → 不可用，不再派
	s.DaemonConnected(ctx, d2, nil)
	s.CreateSession(ctx, u, ag.ID, "", "", json.RawMessage(`{}`))
	if _, err := s.ClaimSession(ctx, d2, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim unavailable: %v", err)
	}
}

func TestAgents(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	daemon := func(u User, name string, hs ...string) Daemon {
		d, _ := s.CreateDaemon(ctx, u.ID, name)
		d, _, _ = s.RegisterDaemon(ctx, d.SetupToken, name)
		var rep []ReportedHarness
		for _, h := range hs {
			rep = append(rep, ReportedHarness{Name: h})
		}
		s.DaemonConnected(ctx, d, rep)
		return d
	}
	laptop := daemon(alice, "laptop", "claude")
	desktop := daemon(alice, "desktop", "codex")
	bobPC := daemon(bob, "bob-pc", "claude", "codex")

	ag, err := s.CreateAgent(ctx, alice.ID, "coder", "写代码", laptop.ID, "claude", "")
	if err != nil || ag.ID != "coder" {
		t.Fatalf("create: %v %+v", err, ag)
	}
	if _, err := s.CreateAgent(ctx, alice.ID, "", "写代码", desktop.ID, "codex", ""); err == nil {
		t.Fatal("duplicate name should fail")
	}
	// 运行时只能是自己的 daemon、上报过的 harness
	if _, err := s.CreateAgent(ctx, alice.ID, "", "x", laptop.ID, "codex", ""); err == nil {
		t.Fatal("harness not on this daemon should fail")
	}
	if _, err := s.CreateAgent(ctx, alice.ID, "", "x", bobPC.ID, "claude", ""); err == nil {
		t.Fatal("someone else's daemon should fail")
	}
	// id 全局唯一：别人也不能再用
	if _, err := s.CreateAgent(ctx, bob.ID, "coder", "别的", bobPC.ID, "codex", ""); err == nil || !strings.Contains(err.Error(), "已被占用") {
		t.Fatalf("duplicate id across users: %v", err)
	}
	if _, err := s.CreateAgent(ctx, bob.ID, "Bad ID", "x", bobPC.ID, "codex", ""); err == nil {
		t.Fatal("bad id should fail")
	}
	if b, err := s.CreateAgent(ctx, bob.ID, "", "写代码", bobPC.ID, "codex", ""); err != nil || !strings.HasPrefix(b.ID, "ag_") {
		t.Fatalf("same name for another user / generated id: %v %+v", err, b)
	}
	// bob 用 alice 的 agent 开会话：会话属于 alice、定在 agent 的运行时上,created_by 是 bob,两人都看得到
	at, _, err := s.CreateSession(ctx, bob, ag.ID, "", "", json.RawMessage(`{}`))
	if err != nil || at.UserID != alice.ID || at.CreatedBy != bob.ID || at.DaemonID != laptop.ID {
		t.Fatalf("bob uses alice's agent: %v %+v", err, at)
	}
	carol, _, _ := s.CreateUser(ctx, "", "carol", RoleUser)
	for _, u := range []User{alice, bob} {
		if _, err := s.GetSession(ctx, u, at.ID); err != nil {
			t.Fatalf("%s get session: %v", u.Name, err)
		}
	}
	if _, err := s.GetSession(ctx, carol, at.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("carol get session: %v", err)
	}
	if list, _ := s.ListSessions(ctx, bob, 10); len(list) != 1 || list[0].ID != at.ID {
		t.Fatalf("bob list: %+v", list)
	}
	// 但只有主人能改、删 agent
	bobName := "bob 的"
	if _, err := s.UpdateAgent(ctx, bob, ag.ID, AgentPatch{Name: &bobName}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob update: %v", err)
	}

	// 切换运行时:daemon 和 harness 一起换，只给一个不行;老会话不变，新会话用新的
	old, _, _ := s.CreateSession(ctx, alice, ag.ID, "", "", json.RawMessage(`{}`))
	codex, desktopID := "codex", desktop.ID
	if _, err := s.UpdateAgent(ctx, alice, ag.ID, AgentPatch{Harness: &codex}); err == nil {
		t.Fatal("harness alone should fail")
	}
	if ag, err = s.UpdateAgent(ctx, alice, ag.ID, AgentPatch{DaemonID: &desktopID, Harness: &codex}); err != nil ||
		ag.DaemonID != desktop.ID || ag.Harness != "codex" || ag.Name != "写代码" {
		t.Fatalf("switch runtime: %v %+v", err, ag)
	}
	claude := "claude"
	if _, err := s.UpdateAgent(ctx, alice, ag.ID, AgentPatch{DaemonID: &desktopID, Harness: &claude}); err == nil {
		t.Fatal("harness not on that daemon should fail")
	}
	nw, _, _ := s.CreateSession(ctx, alice, ag.ID, "", "", json.RawMessage(`{}`))
	old, _ = s.GetSession(ctx, alice, old.ID)
	if old.DaemonID != laptop.ID || old.Harness != "claude" || nw.DaemonID != desktop.ID || nw.Harness != "codex" {
		t.Fatalf("runtime after switch: old=%+v new=%+v", old, nw)
	}
	s.SetDaemonHarnessEnabled(ctx, alice, laptop.ID, "claude", true)
	if got, err := s.ClaimSession(ctx, laptop, 60); err != nil || got.Session.ID != at.ID {
		t.Fatalf("laptop claims its session: %v %+v", err, got)
	}

	// 有 agent 用着的 harness 不能删
	s.CreateCustomHarness(ctx, alice.ID, "mine", "", "")
	s.DaemonConnected(ctx, desktop, []ReportedHarness{{Name: "codex"}, {Name: "mine"}})
	mine := "mine"
	if _, err := s.UpdateAgent(ctx, alice, ag.ID, AgentPatch{DaemonID: &desktopID, Harness: &mine}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCustomHarness(ctx, alice, "mine"); err == nil {
		t.Fatal("harness used by agent should not be deletable")
	}

	// 删 agent:会话还在,agent_id 变空
	if err := s.DeleteAgent(ctx, bob, ag.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob delete: %v", err)
	}
	if err := s.DeleteAgent(ctx, alice, ag.ID); err != nil {
		t.Fatal(err)
	}
	if old, err = s.GetSession(ctx, alice, old.ID); err != nil || old.AgentID != "" || old.Harness != "claude" {
		t.Fatalf("session after agent delete: %v %+v", err, old)
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
