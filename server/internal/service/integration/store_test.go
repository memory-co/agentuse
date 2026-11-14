package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/memory-co/agentuse/server/internal/service"
)

// newStore 开一个内存库，测试结束时关掉。时钟每读一次走一秒，按时间排序的结果是确定的。
func newStore(t *testing.T) *service.Service {
	t.Helper()
	s, err := service.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Unix(1_000_000, 0)
	s.Now = func() time.Time { now = now.Add(time.Second); return now }
	return s
}

// registered 建一台已注册的 device,并上报 harnesses。
func registered(t *testing.T, s *service.Service, u service.User, name string, harnesses ...string) service.Device {
	t.Helper()
	ctx := context.Background()
	d, err := s.CreateDevice(ctx, u.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	if d, _, err = s.RegisterDevice(ctx, d.SetupToken, name); err != nil {
		t.Fatal(err)
	}
	var rep []service.ReportedHarness
	for _, h := range harnesses {
		rep = append(rep, service.ReportedHarness{Name: h})
	}
	if _, err := s.DeviceConnected(ctx, d, rep); err != nil {
		t.Fatal(err)
	}
	for _, h := range harnesses {
		if err := s.SetDeviceHarnessEnabled(ctx, u, d.ID, h, true); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	s, err := service.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, _, err := s.CreateUser(ctx, "u1", "alice", service.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 重开：数据还在，内置 harness 重复写入不报错
	s, err = service.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.GetUser(ctx, u.ID); err != nil || got.Name != "alice" {
		t.Fatalf("reopen: %v %+v", err, got)
	}
	hs, _ := s.ListHarnesses(ctx)
	if len(hs) != 0 {
		t.Fatalf("unreported harnesses after reopen: %+v", hs)
	}
}

func TestSecrets(t *testing.T) {
	a, b := service.RandomToken("ut_"), service.RandomToken("ut_")
	if a == b || len(a) != 3+48 || a[:3] != "ut_" {
		t.Fatalf("tokens: %q %q", a, b)
	}
	if service.HashSecret(a) == service.HashSecret(b) || service.HashSecret(a) != service.HashSecret(a) || len(service.HashSecret(a)) != 64 {
		t.Fatal("hash")
	}
	if id := randomID("dev_"); len(id) != len("dev_")+16 {
		t.Fatalf("randomID: %q", id)
	}
	if u := newUUID(); len(u) != 36 || u[14] != '4' {
		t.Fatalf("uuid v4: %q", u)
	}
}
