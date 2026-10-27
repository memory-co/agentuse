package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDaemonSetup(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)

	d, err := s.CreateDaemon(ctx, alice.ID, "  ")
	if err != nil || d.Status != DaemonPending || d.SetupToken == "" || d.Name != "" {
		t.Fatalf("create: %v %+v", err, d)
	}
	if _, _, err := s.RegisterDaemon(ctx, "st_nope", "h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad setup token: %v", err)
	}
	// 没起名字：用主机名;主机名也没有就用 id
	reg, key, err := s.RegisterDaemon(ctx, d.SetupToken, "")
	if err != nil || reg.Name != d.ID || reg.Status != DaemonOffline {
		t.Fatalf("register: %v %+v", err, reg)
	}
	if _, _, err := s.RegisterDaemon(ctx, d.SetupToken, "h"); !errors.Is(err, ErrNotFound) {
		t.Fatal("setup token is single-use")
	}
	if got, err := s.DaemonByKey(ctx, key); err != nil || got.ID != d.ID || got.SetupToken != "" {
		t.Fatalf("by key: %v %+v", err, got)
	}

	// 重置：新安装令牌，旧密钥失效;别人重置不了
	if _, err := s.ResetDaemonSetup(ctx, bob, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob reset: %v", err)
	}
	reset, err := s.ResetDaemonSetup(ctx, alice, d.ID)
	if err != nil || reset.Status != DaemonPending || reset.SetupToken == "" || reset.SetupToken == d.SetupToken {
		t.Fatalf("reset: %v %+v", err, reset)
	}
	if _, err := s.DaemonByKey(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("old key should be invalid after reset")
	}
	if again, _, err := s.RegisterDaemon(ctx, reset.SetupToken, "other-host"); err != nil || again.Name != d.ID {
		t.Fatalf("re-register keeps name: %v %+v", err, again)
	}
}

func TestDaemonStatus(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	root, _, _ := s.CreateUser(ctx, "", "root", RoleAdmin)
	d := registered(t, s, alice, "laptop", "claude", "codex")
	b := registered(t, s, bob, "bob-pc")

	got, err := s.GetDaemon(ctx, alice, d.ID)
	if err != nil || got.Status != DaemonOnline || got.LastSeenAt == nil || len(got.Harnesses) != 2 {
		t.Fatalf("online: %v %+v", err, got)
	}
	if h := got.Harnesses[0]; h.Harness != "claude" || !h.Available || !h.Enabled || h.UserID != alice.ID {
		t.Fatalf("reported harness: %+v", h)
	}
	if _, err := s.GetDaemon(ctx, bob, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob sees alice's daemon: %v", err)
	}
	if _, err := s.GetDaemon(ctx, root, d.ID); err != nil {
		t.Fatalf("admin sees all: %v", err)
	}
	if l, _ := s.ListDaemons(ctx, alice); len(l) != 1 || len(l[0].Harnesses) != 2 {
		t.Fatalf("alice list: %+v", l)
	}
	if l, _ := s.ListDaemons(ctx, root); len(l) != 2 || l[1].ID != b.ID || len(l[1].Harnesses) != 0 {
		t.Fatalf("admin list: %+v", l)
	}

	// 开关：只能开自己的、上报过的
	if err := s.SetDaemonHarnessEnabled(ctx, bob, d.ID, "claude", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob toggles: %v", err)
	}
	if err := s.SetDaemonHarnessEnabled(ctx, alice, d.ID, "ghost", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreported harness: %v", err)
	}
	if err := s.SetDaemonHarnessEnabled(ctx, root, d.ID, "codex", true); err != nil {
		t.Fatal(err)
	}
	// 重连少报了 claude:标不可用，codex 的 enabled 保留
	s.DaemonConnected(ctx, d, []ReportedHarness{{Name: "codex", Version: "2"}})
	got, _ = s.GetDaemon(ctx, alice, d.ID)
	if got.Harnesses[0].Available || !got.Harnesses[1].Available || !got.Harnesses[1].Enabled || got.Harnesses[1].Version != "2" {
		t.Fatalf("after reconnect: %+v", got.Harnesses)
	}

	seen := *got.LastSeenAt
	s.TouchDaemon(ctx, d.ID)
	if got, _ = s.GetDaemon(ctx, alice, d.ID); *got.LastSeenAt <= seen {
		t.Fatal("touch should bump last_seen_at")
	}
	s.DaemonDisconnected(ctx, d.ID)
	if got, _ = s.GetDaemon(ctx, alice, d.ID); got.Status != DaemonOffline {
		t.Fatalf("disconnected: %+v", got)
	}
	// pending 的不会被断线改成 offline
	p, _ := s.CreateDaemon(ctx, alice.ID, "new")
	s.DaemonDisconnected(ctx, p.ID)
	if got, _ = s.GetDaemon(ctx, alice, p.ID); got.Status != DaemonPending {
		t.Fatalf("pending after disconnect: %+v", got)
	}

	s.DaemonConnected(ctx, d, nil)
	s.MarkAllDaemonsOffline(ctx)
	all, _ := s.ListDaemons(ctx, root)
	for _, d := range all {
		if d.Status == DaemonOnline {
			t.Fatalf("still online after MarkAllDaemonsOffline: %+v", d)
		}
	}
}

func TestDeleteDaemon(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	d := registered(t, s, alice, "laptop", "claude")
	ag, _ := s.CreateAgent(ctx, alice.ID, "", "a", d.ID, "claude", "")
	ss, _, _ := s.CreateSession(ctx, alice, ag.ID, "", json.RawMessage(`{}`))

	if err := s.DeleteDaemon(ctx, bob, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob deletes: %v", err)
	}
	if err := s.DeleteDaemon(ctx, alice, "dm_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := s.DeleteDaemon(ctx, alice, d.ID); err != nil {
		t.Fatal(err)
	}
	// 排队中的会话记一条失败回复、回到 idle;daemon_harnesses 跟着删
	got, _ := s.GetSession(ctx, alice, ss.ID)
	msgs, _ := s.Messages(ctx, ss.ID)
	if got.Status != SessionIdle || len(msgs) != 2 || string(msgs[1].Content) != `{"ok":false,"error":"daemon 已删除"}` {
		t.Fatalf("pending session after delete: %+v %+v", got, msgs)
	}
	if l, _ := s.ListDaemons(ctx, alice); len(l) != 0 {
		t.Fatalf("list after delete: %+v", l)
	}
}
