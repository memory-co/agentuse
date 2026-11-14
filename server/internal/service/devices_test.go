package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestDeviceSetup(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)

	d, err := s.CreateDevice(ctx, alice.ID, "  ")
	if err != nil || d.Status != DevicePending || d.SetupToken == "" || d.Name != "" {
		t.Fatalf("create: %v %+v", err, d)
	}
	if _, _, err := s.RegisterDevice(ctx, "st_nope", "h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad setup token: %v", err)
	}
	// 没起名字：用主机名;主机名也没有就用 id
	reg, key, err := s.RegisterDevice(ctx, d.SetupToken, "")
	if err != nil || reg.Name != d.ID || reg.Status != DeviceOffline {
		t.Fatalf("register: %v %+v", err, reg)
	}
	if _, _, err := s.RegisterDevice(ctx, d.SetupToken, "h"); !errors.Is(err, ErrNotFound) {
		t.Fatal("setup token is single-use")
	}
	if got, err := s.DeviceByKey(ctx, key); err != nil || got.ID != d.ID || got.SetupToken != "" {
		t.Fatalf("by key: %v %+v", err, got)
	}

	// 重置：新安装令牌，旧密钥失效;别人重置不了
	if _, err := s.ResetDeviceSetup(ctx, bob, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob reset: %v", err)
	}
	reset, err := s.ResetDeviceSetup(ctx, alice, d.ID)
	if err != nil || reset.Status != DevicePending || reset.SetupToken == "" || reset.SetupToken == d.SetupToken {
		t.Fatalf("reset: %v %+v", err, reset)
	}
	if _, err := s.DeviceByKey(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("old key should be invalid after reset")
	}
	if again, _, err := s.RegisterDevice(ctx, reset.SetupToken, "other-host"); err != nil || again.Name != d.ID {
		t.Fatalf("re-register keeps name: %v %+v", err, again)
	}
}

func TestDeviceStatus(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	root, _, _ := s.CreateUser(ctx, "", "root", RoleAdmin)
	d := registered(t, s, alice, "laptop", "claude", "codex")
	b := registered(t, s, bob, "bob-pc")

	got, err := s.GetDevice(ctx, alice, d.ID)
	if err != nil || got.Status != DeviceOnline || got.LastSeenAt == nil || len(got.Harnesses) != 2 {
		t.Fatalf("online: %v %+v", err, got)
	}
	if h := got.Harnesses[0]; h.Harness != "claude" || !h.Available || !h.Enabled || h.UserID != alice.ID {
		t.Fatalf("reported harness: %+v", h)
	}
	if _, err := s.GetDevice(ctx, bob, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob sees alice's device: %v", err)
	}
	if _, err := s.GetDevice(ctx, root, d.ID); err != nil {
		t.Fatalf("admin sees all: %v", err)
	}
	if l, _ := s.ListDevices(ctx, alice); len(l) != 1 || len(l[0].Harnesses) != 2 {
		t.Fatalf("alice list: %+v", l)
	}
	if l, _ := s.ListDevices(ctx, root); len(l) != 2 || l[1].ID != b.ID || len(l[1].Harnesses) != 0 {
		t.Fatalf("admin list: %+v", l)
	}

	// 开关：只能开自己的、上报过的
	if err := s.SetDeviceHarnessEnabled(ctx, bob, d.ID, "claude", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob toggles: %v", err)
	}
	if err := s.SetDeviceHarnessEnabled(ctx, alice, d.ID, "ghost", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreported harness: %v", err)
	}
	if err := s.SetDeviceHarnessEnabled(ctx, root, d.ID, "codex", true); err != nil {
		t.Fatal(err)
	}
	// 重连少报了 claude:标不可用，codex 的 enabled 保留
	s.DeviceConnected(ctx, d, []ReportedHarness{{Name: "codex", Version: "2"}})
	got, _ = s.GetDevice(ctx, alice, d.ID)
	if got.Harnesses[0].Available || !got.Harnesses[1].Available || !got.Harnesses[1].Enabled || got.Harnesses[1].Version != "2" {
		t.Fatalf("after reconnect: %+v", got.Harnesses)
	}

	seen := *got.LastSeenAt
	s.TouchDevice(ctx, d.ID)
	if got, _ = s.GetDevice(ctx, alice, d.ID); *got.LastSeenAt <= seen {
		t.Fatal("touch should bump last_seen_at")
	}
	s.DeviceDisconnected(ctx, d.ID)
	if got, _ = s.GetDevice(ctx, alice, d.ID); got.Status != DeviceOffline {
		t.Fatalf("disconnected: %+v", got)
	}
	// pending 的不会被断线改成 offline
	p, _ := s.CreateDevice(ctx, alice.ID, "new")
	s.DeviceDisconnected(ctx, p.ID)
	if got, _ = s.GetDevice(ctx, alice, p.ID); got.Status != DevicePending {
		t.Fatalf("pending after disconnect: %+v", got)
	}

	s.DeviceConnected(ctx, d, nil)
	s.MarkAllDevicesOffline(ctx)
	all, _ := s.ListDevices(ctx, root)
	for _, d := range all {
		if d.Status == DeviceOnline {
			t.Fatalf("still online after MarkAllDevicesOffline: %+v", d)
		}
	}
}

func TestDeviceReportsKeepUserEnablementAndCapabilities(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	report := []ReportedHarness{{Name: a.Harness, Version: "2", Revision: "package-v2", Capabilities: []string{"file.read", "file.write", "file.read"}}}
	if _, err := s.DeviceConnected(ctx, d, report); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetDevice(ctx, u, d.ID)
	if err != nil || len(current.Harnesses) != 1 {
		t.Fatal(current, err)
	}
	h := current.Harnesses[0]
	if !h.Enabled || !h.Available || h.HarnessRevision != "package-v2" || len(h.Capabilities) != 2 {
		t.Fatal("report lost user settings or metadata", h)
	}
	if err = s.SetDeviceHarnessEnabled(ctx, u, d.ID, a.Harness, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeviceConnected(ctx, d, report); err != nil {
		t.Fatal(err)
	}
	current, err = s.GetDevice(ctx, u, d.ID)
	if err != nil || current.Harnesses[0].Enabled {
		t.Fatal("heartbeat enabled a disabled harness", current, err)
	}
}

func TestDeviceDeletionPreservesActualRunsAndFixedVersions(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	live, _, err := s.CreateConversation(ctx, u, "", "", "live", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "native", 1, acceptedEvent(run, "one", "live"))
	sent, _, err := s.CreateConversation(ctx, u, "", "", "uncertain", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DispatchMessage(ctx, d); err != nil {
		t.Fatal(err)
	}
	pending, _, err := s.CreateConversation(ctx, u, "", "", "pending", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDevice(ctx, u, d.ID); err != nil {
		t.Fatal(err)
	}
	actual, err := s.GetRun(ctx, u, live.ID, run)
	if err != nil || actual.Status != "running" || actual.ResultMessageID != nil {
		t.Fatal("device deletion invented a native result", actual, err)
	}
	for id, want := range map[string]string{sent.ID: "unknown", pending.ID: "rejected"} {
		targets, err := s.Deliveries(ctx, id)
		if err != nil || len(targets) != 1 || targets[0].Status != want {
			t.Fatal(id, targets, err)
		}
	}
	version, err := s.GetAgentVersion(ctx, u, a.ID, a.CurrentVersionID)
	if err != nil || version.DeviceID != d.ID {
		t.Fatal("historical device binding was changed", version, err)
	}
}
