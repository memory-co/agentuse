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

	u, _, _ := s.CreateUser(ctx, "alice", RoleUser)
	d, _ := s.CreateDaemon(ctx, u.ID, "")
	d, _, err = s.RegisterDaemon(ctx, d.SetupToken, "host-a")
	if err != nil || d.Name != "host-a" {
		t.Fatalf("register: %v %+v", err, d)
	}
	if _, err := s.CreateTask(ctx, u.ID, "nope", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown agent should fail")
	}
	task, _ := s.CreateTask(ctx, u.ID, "claude", json.RawMessage(`{"prompt":"x"}`))

	// 没上报 / 没开启都领不到
	if _, err := s.ClaimTask(ctx, d, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim before report: %v", err)
	}
	ignored, _ := s.DaemonConnected(ctx, d, []ReportedAgent{{Name: "claude", Version: "1.0"}, {Name: "ghost"}})
	if len(ignored) != 1 || ignored[0] != "ghost" {
		t.Fatalf("ignored: %v", ignored)
	}
	if _, err := s.ClaimTask(ctx, d, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim before enable: %v", err)
	}
	if err := s.SetDaemonAgentEnabled(ctx, u, d.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
	// 重连上报不会丢掉 enabled
	s.DaemonConnected(ctx, d, []ReportedAgent{{Name: "claude", Version: "1.1"}})
	got, err := s.ClaimTask(ctx, d, 60)
	if err != nil || got.ID != task.ID || got.Status != TaskRunning {
		t.Fatalf("claim: %v %+v", err, got)
	}

	// 租约过期 → 回到 pending
	now = now.Add(61 * time.Second)
	users, _ := s.RequeueExpired(ctx)
	if len(users) != 1 || users[0] != u.ID {
		t.Fatalf("requeue: %v", users)
	}
	if err := s.AddEvent(ctx, d.ID, task.ID, EventProgress, nil, 60); !errors.Is(err, ErrStaleTask) {
		t.Fatalf("stale progress: %v", err)
	}
	got, _ = s.ClaimTask(ctx, d, 60)
	if err := s.AddEvent(ctx, d.ID, got.ID, EventDone, json.RawMessage(`{"ok":true}`), 60); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTask(ctx, u, task.ID)
	if got.Status != TaskDone || got.FinishedAt == nil || string(got.Result) != `{"ok":true}` {
		t.Fatalf("done: %+v", got)
	}

	// 本机不再上报 claude → 不可用，不再派
	s.DaemonConnected(ctx, d, nil)
	s.CreateTask(ctx, u.ID, "claude", json.RawMessage(`{}`))
	if _, err := s.ClaimTask(ctx, d, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim unavailable: %v", err)
	}
}
