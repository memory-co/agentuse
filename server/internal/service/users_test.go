package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCreateUserID(t *testing.T) {
	s := newStore(t)
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

func TestUsers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("empty count: %d", n)
	}
	if _, err := s.FirstAdmin(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no admin yet: %v", err)
	}
	if _, _, err := s.CreateUser(ctx, "", "  ", RoleUser); err == nil {
		t.Fatal("empty name should fail")
	}
	if _, _, err := s.CreateUser(ctx, "", "x", "root"); err == nil {
		t.Fatal("unknown role should fail")
	}

	alice, aliceTok, _ := s.CreateUser(ctx, "", " alice ", RoleUser)
	root, rootTok, _ := s.CreateUser(ctx, "", "root", RoleAdmin)
	s.CreateUser(ctx, "", "root2", RoleAdmin)
	if alice.Name != "alice" || alice.IsAdmin() || !root.IsAdmin() || !strings.HasPrefix(aliceTok, "ut_") {
		t.Fatalf("created: %+v %+v %q", alice, root, aliceTok)
	}
	if n, _ := s.CountUsers(ctx); n != 3 {
		t.Fatalf("count: %d", n)
	}
	if a, err := s.FirstAdmin(ctx); err != nil || a.ID != root.ID {
		t.Fatalf("first admin: %v %+v", err, a)
	}
	if u, err := s.UserByToken(ctx, rootTok); err != nil || u.ID != root.ID {
		t.Fatalf("by token: %v %+v", err, u)
	}
	if _, err := s.UserByToken(ctx, "ut_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad token: %v", err)
	}
	list, _ := s.ListUsers(ctx)
	if len(list) != 3 || list[0].ID != alice.ID || list[1].ID != root.ID {
		t.Fatalf("list by created_at: %+v", list)
	}

	// 换令牌：旧的立即失效
	u, newTok, err := s.ResetUserToken(ctx, alice.ID)
	if err != nil || u.ID != alice.ID || newTok == aliceTok {
		t.Fatalf("reset: %v %+v", err, u)
	}
	if _, err := s.UserByToken(ctx, aliceTok); !errors.Is(err, ErrNotFound) {
		t.Fatal("old token should be invalid")
	}
	if got, err := s.UserByToken(ctx, newTok); err != nil || got.ID != alice.ID {
		t.Fatalf("new token: %v", err)
	}
	if _, _, err := s.ResetUserToken(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset unknown: %v", err)
	}
}
