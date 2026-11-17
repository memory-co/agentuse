package test

import (
	"context"
	"github.com/memory-co/agentuse/internal/securews"
	"path/filepath"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/testdb"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/service"
	deviceservice "github.com/memory-co/agentuse/server/internal/service/device"
)

// newService 开一个内存库，测试结束时关掉。时钟每读一次走一秒，按时间排序的结果是确定的。
func newService(t *testing.T) *service.Service {
	t.Helper()
	driver, dsn := testdb.Config(t)
	db, err := database.OpenConfig(database.Config{Driver: driver, DSN: dsn})
	var s *service.Service
	if err == nil {
		s = service.New(db)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Unix(1_000_000, 0)
	s.Now = func() time.Time { now = now.Add(time.Second); return now }
	if err := s.ConfigureSecurity(service.SecurityConfig{}); err != nil {
		t.Fatal(err)
	}
	return s
}

// registered 建一台已注册的 device,并上报 harnesses。
func registered(t *testing.T, s *service.Service, u agentuse.User, name string, harnesses ...string) agentuse.Device {
	t.Helper()
	ctx := context.Background()
	d, err := s.DeviceService.CreateDevice(ctx, u.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	_, csr, err := securews.NewCredentials(s.Security.Identity.CA)
	if err != nil {
		t.Fatal(err)
	}
	if d, _, err = s.DeviceService.Enroll(ctx, d.SetupToken, name, csr); err != nil {
		t.Fatal(err)
	}
	var rep []deviceservice.ReportedHarness
	for _, h := range harnesses {
		rep = append(rep, deviceservice.ReportedHarness{Name: h})
	}
	if _, err := s.DeviceService.DeviceConnected(ctx, d, rep); err != nil {
		t.Fatal(err)
	}
	for _, h := range harnesses {
		if err := s.DeviceService.SetDeviceHarnessEnabled(ctx, u, d.ID, h, true); err != nil {
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
	u, _, err := s.UserService.CreateUser(ctx, "u1", "alice", agentuse.RoleUser)
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
	if got, err := s.UserService.GetUser(ctx, u.ID); err != nil || got.Name != "alice" {
		t.Fatalf("reopen: %v %+v", err, got)
	}
	hs, _ := s.DeviceService.ListHarnesses(ctx)
	if len(hs) != 0 {
		t.Fatalf("unreported harnesses after reopen: %+v", hs)
	}
}
