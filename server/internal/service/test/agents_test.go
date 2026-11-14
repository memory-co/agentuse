package test

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestAgents(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", service.RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", service.RoleUser)
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
	// 运行时只能是自己的 device、上报过的 harness
	if _, err := s.CreateAgent(ctx, alice.ID, "", "x", laptop.ID, "codex", ""); err == nil {
		t.Fatal("harness not on this device should fail")
	}
	if _, err := s.CreateAgent(ctx, alice.ID, "", "x", bobPC.ID, "claude", ""); err == nil {
		t.Fatal("someone else's device should fail")
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
	// Only the owner may publish a full immutable configuration.
	config := agentuse.AgentConfig{Name: ag.Name, DeviceID: desktop.ID, Harness: "codex"}
	if _, err := s.PublishAgentVersion(ctx, bob, ag.ID, ag.CurrentVersionID, config); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("bob publish: %v", err)
	}
	old := ag.CurrentVersionID
	if ag, err = s.PublishAgentVersion(ctx, alice, ag.ID, old, config); err != nil || ag.DeviceID != desktop.ID || ag.Harness != "codex" || ag.Version != 2 {
		t.Fatalf("switch runtime: %v %+v", err, ag)
	}
	previous, err := s.GetAgentVersion(ctx, alice, ag.ID, old)
	if err != nil || previous.DeviceID != laptop.ID || previous.Harness != "claude" {
		t.Fatalf("old version changed: %+v %v", previous, err)
	}
	config.Harness = "claude"
	if _, err := s.PublishAgentVersion(ctx, alice, ag.ID, ag.CurrentVersionID, config); err == nil {
		t.Fatal("unreported harness accepted")
	}
	s.DeviceConnected(ctx, desktop, []service.ReportedHarness{{Name: "codex"}, {Name: "mine"}})
	s.SetDeviceHarnessEnabled(ctx, alice, desktop.ID, "mine", true)
	config.Harness = "mine"
	if _, err := s.PublishAgentVersion(ctx, alice, ag.ID, ag.CurrentVersionID, config); err != nil {
		t.Fatal(err)
	}

	// Soft deletion retains immutable history.
	if err := s.DeleteAgent(ctx, bob, ag.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("bob delete: %v", err)
	}
	if err := s.DeleteAgent(ctx, alice, ag.ID); err != nil {
		t.Fatal(err)
	}
}
