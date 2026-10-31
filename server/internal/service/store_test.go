package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// newStore 开一个内存库，测试结束时关掉。时钟每读一次走一秒，按时间排序的结果是确定的。
func newStore(t *testing.T) *Service {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Unix(1_000_000, 0)
	s.now = func() time.Time { now = now.Add(time.Second); return now }
	return s
}

// registered 建一台已注册的 daemon,并上报 harnesses。
func registered(t *testing.T, s *Service, u User, name string, harnesses ...string) Daemon {
	t.Helper()
	ctx := context.Background()
	d, err := s.CreateDaemon(ctx, u.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	if d, _, err = s.RegisterDaemon(ctx, d.SetupToken, name); err != nil {
		t.Fatal(err)
	}
	var rep []ReportedHarness
	for _, h := range harnesses {
		rep = append(rep, ReportedHarness{Name: h})
	}
	if _, err := s.DaemonConnected(ctx, d, rep); err != nil {
		t.Fatal(err)
	}
	for _, h := range harnesses {
		if err := s.SetDaemonHarnessEnabled(ctx, u, d.ID, h, true); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, _, err := s.CreateUser(ctx, "u1", "alice", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 重开：数据还在，内置 harness 重复写入不报错
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.GetUser(ctx, u.ID); err != nil || got.Name != "alice" {
		t.Fatalf("reopen: %v %+v", err, got)
	}
	hs, _ := s.ListHarnesses(ctx)
	if len(hs) != 2 {
		t.Fatalf("builtin harnesses after reopen: %+v", hs)
	}
}

func TestSecrets(t *testing.T) {
	a, b := RandomToken("ut_"), RandomToken("ut_")
	if a == b || len(a) != 3+48 || a[:3] != "ut_" {
		t.Fatalf("tokens: %q %q", a, b)
	}
	if HashSecret(a) == HashSecret(b) || HashSecret(a) != HashSecret(a) || len(HashSecret(a)) != 64 {
		t.Fatal("hash")
	}
	if id := randomID("dm_"); len(id) != 3+16 {
		t.Fatalf("randomID: %q", id)
	}
	if u := newUUID(); len(u) != 36 || u[14] != '4' {
		t.Fatalf("uuid v4: %q", u)
	}
}
