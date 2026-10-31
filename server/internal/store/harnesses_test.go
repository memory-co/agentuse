package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHarnesses(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	root, _, _ := s.CreateUser(ctx, "", "root", RoleAdmin)

	// 内置的来自 harness/*/harness.yaml
	hs, _ := s.ListHarnesses(ctx)
	if len(hs) != 2 || hs[0].Name != "claude" || hs[0].Kind != HarnessBuiltin || hs[0].DisplayName != "Claude Code" || hs[1].Name != "codex" {
		t.Fatalf("builtins: %+v", hs)
	}

	for _, bad := range []string{"", "Upper", "-dash", "has space", "under_score"} {
		if _, err := s.CreateCustomHarness(ctx, alice.ID, bad, "", ""); err == nil {
			t.Fatalf("bad name %q should fail", bad)
		}
	}
	if _, err := s.CreateCustomHarness(ctx, alice.ID, "claude", "", ""); err == nil {
		t.Fatal("builtin name should be taken")
	}
	h, err := s.CreateCustomHarness(ctx, alice.ID, " mine ", "", "my harness")
	if err != nil || h.Name != "mine" || h.DisplayName != "mine" || h.Kind != HarnessCustom || h.OwnerID != alice.ID {
		t.Fatalf("create: %v %+v", err, h)
	}
	if _, err := s.CreateCustomHarness(ctx, bob.ID, "mine", "", ""); err == nil {
		t.Fatal("duplicate should fail")
	}
	s.CreateCustomHarness(ctx, bob.ID, "a-bob", "Bob 的", "")

	// 内置在前，自定义在后，各自按名字
	hs, _ = s.ListHarnesses(ctx)
	var names []string
	for _, h := range hs {
		names = append(names, h.Name)
	}
	if want := "claude codex a-bob mine"; strings.Join(names, " ") != want {
		t.Fatalf("order: %v", names)
	}

	if err := s.DeleteCustomHarness(ctx, bob, "mine"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob deletes alice's: %v", err)
	}
	if err := s.DeleteCustomHarness(ctx, root, "claude"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("builtin is not deletable: %v", err)
	}
	if err := s.DeleteCustomHarness(ctx, alice, "mine"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCustomHarness(ctx, root, "a-bob"); err != nil {
		t.Fatalf("admin deletes anyone's: %v", err)
	}
	if hs, _ = s.ListHarnesses(ctx); len(hs) != 2 {
		t.Fatalf("after delete: %+v", hs)
	}
}
