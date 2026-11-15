package test

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

func TestCreateUserID(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	u, _, err := s.UserService.CreateUser(ctx, "u_10086", "alice", agentuse.RoleUser)
	if err != nil || u.ID != "u_10086" {
		t.Fatalf("custom id: %v %+v", err, u)
	}
	if _, _, err := s.UserService.CreateUser(ctx, "u_10086", "bob", agentuse.RoleUser); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("duplicate id: %v", err)
	}
	if _, _, err := s.UserService.CreateUser(ctx, "u_2", "alice", agentuse.RoleUser); err == nil || !strings.Contains(err.Error(), "用户名") {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, _, err := s.UserService.CreateUser(ctx, "has space", "carol", agentuse.RoleUser); err == nil {
		t.Fatal("invalid id should fail")
	}
	g, _, err := s.UserService.CreateUser(ctx, "", "dave", agentuse.RoleUser)
	if err != nil || len(g.ID) != 36 {
		t.Fatalf("generated id: %v %+v", err, g)
	}
}

func TestUsers(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	if n, _ := s.UserService.CountUsers(ctx); n != 0 {
		t.Fatalf("empty count: %d", n)
	}
	if _, err := s.UserService.FirstAdmin(ctx); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("no admin yet: %v", err)
	}
	if _, _, err := s.UserService.CreateUser(ctx, "", "  ", agentuse.RoleUser); err == nil {
		t.Fatal("empty name should fail")
	}
	if _, _, err := s.UserService.CreateUser(ctx, "", "x", "root"); err == nil {
		t.Fatal("unknown role should fail")
	}

	alice, aliceTok, _ := s.UserService.CreateUser(ctx, "", " alice ", agentuse.RoleUser)
	root, rootTok, _ := s.UserService.CreateUser(ctx, "", "root", agentuse.RoleAdmin)
	s.UserService.CreateUser(ctx, "", "root2", agentuse.RoleAdmin)
	if alice.Name != "alice" || alice.IsAdmin() || !root.IsAdmin() || !strings.HasPrefix(aliceTok, "ut_") {
		t.Fatalf("created: %+v %+v %q", alice, root, aliceTok)
	}
	if n, _ := s.UserService.CountUsers(ctx); n != 3 {
		t.Fatalf("count: %d", n)
	}
	if a, err := s.UserService.FirstAdmin(ctx); err != nil || a.ID != root.ID {
		t.Fatalf("first admin: %v %+v", err, a)
	}
	if u, err := s.UserService.UserByToken(ctx, rootTok); err != nil || u.ID != root.ID {
		t.Fatalf("by token: %v %+v", err, u)
	}
	if _, err := s.UserService.UserByToken(ctx, "ut_nope"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("bad token: %v", err)
	}
	list, _ := s.UserService.ListUsers(ctx)
	if len(list) != 3 || list[0].ID != alice.ID || list[1].ID != root.ID {
		t.Fatalf("list by created_at: %+v", list)
	}

	// 换令牌：旧的立即失效
	u, newTok, err := s.UserService.ResetUserToken(ctx, alice.ID)
	if err != nil || u.ID != alice.ID || newTok == aliceTok {
		t.Fatalf("reset: %v %+v", err, u)
	}
	if _, err := s.UserService.UserByToken(ctx, aliceTok); !errors.Is(err, database.ErrNotFound) {
		t.Fatal("old token should be invalid")
	}
	if got, err := s.UserService.UserByToken(ctx, newTok); err != nil || got.ID != alice.ID {
		t.Fatalf("new token: %v", err)
	}
	if _, _, err := s.UserService.ResetUserToken(ctx, "nobody"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("reset unknown: %v", err)
	}
}
