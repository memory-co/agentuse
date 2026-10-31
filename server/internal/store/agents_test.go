package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAgents(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	laptop := registered(t, s, alice, "laptop", "claude")
	desktop := registered(t, s, alice, "desktop", "codex")
	bobPC := registered(t, s, bob, "bob-pc", "claude", "codex")

	ag, err := s.CreateAgent(ctx, alice.ID, "coder", "写代码", laptop.ID, "claude", "")
	if err != nil || ag.ID != "coder" {
		t.Fatalf("create: %v %+v", err, ag)
	}
	if _, err := s.CreateAgent(ctx, alice.ID, "", "写代码", desktop.ID, "codex", ""); err == nil || !strings.Contains(err.Error(), "已经有") {
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
	// 但只有主人能改、删 agent
	bobName := "bob 的"
	if _, err := s.UpdateAgent(ctx, bob, ag.ID, AgentPatch{Name: &bobName}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob update: %v", err)
	}

	// 切换运行时:daemon 和 harness 一起换，只给一个不行;老会话不变，新会话用新的
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
	// 有 agent 用着的 harness 不能删
	s.CreateCustomHarness(ctx, alice.ID, "mine", "", "")
	s.DaemonConnected(ctx, desktop, []ReportedHarness{{Name: "codex"}, {Name: "mine"}})
	s.SetDaemonHarnessEnabled(ctx, alice, desktop.ID, "mine", true)
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
}
