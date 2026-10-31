package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/client/internal/store"
)

func TestSettingsFilesValidateAndRetainLastValidSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agentuse", "setting.yaml")
	s, e := NewSettingService(path)
	if e != nil {
		t.Fatal(e)
	}
	v := s.Get()
	if v.LocalHTTP.Port != 7801 || v.Session.Quota != nil {
		t.Fatal(v)
	}
	v.Session.Quota = &Quota{MaxBytes: 0}
	if e = s.Save(v); e == nil {
		t.Fatal("invalid quota accepted")
	}
	if s.Get().Session.Quota != nil {
		t.Fatal("invalid setting applied")
	}
	os.WriteFile(path, []byte("execution:\n  max_running_runs: 1\n"), 0600)
	if e = s.Reload(); e == nil {
		t.Fatal("obsolete setting accepted")
	}
	if s.Get().LocalHTTP.Port != 7801 {
		t.Fatal("last valid setting lost")
	}
	v = DefaultSettings()
	for _, root := range []string{home, "/", filepath.Join(home, ".agentuse", "harness"), filepath.Join(home, ".claude", "sessions")} {
		v.Session.WorkRoot = root
		if e = v.Validate(); e == nil {
			t.Fatal("unsafe root accepted", root)
		}
	}
}
func TestEndpointYAMLOnlyStoresAddressAndRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endpoints.yaml")
	svc := EndpointService{Path: path}
	for _, ep := range []Endpoint{{Name: "one", ServerURL: "https://Company.example.com:443/", Enabled: true, DeviceID: "d1", DeviceKey: "key1"}, {Name: "two", ServerURL: "http://localhost:8090", Enabled: true, DeviceID: "d2", DeviceKey: "key2"}} {
		if e := svc.Save(ep); e != nil {
			t.Fatal(e)
		}
	}
	all, e := svc.List()
	if e != nil || len(all) != 2 || all[0].ID() != "https---company.example.com" || all[1].ID() != "http---localhost%3A8090" {
		t.Fatal(all, e)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "endpoint_id:") || strings.Contains(string(b), "\n    id:") {
		t.Fatal(string(b))
	}
	ep := all[0]
	ep.DeviceKey = "new-key"
	if e = svc.Save(ep); e != nil {
		t.Fatal(e)
	}
	all, _ = svc.List()
	if len(all) != 2 || all[0].DeviceKey != "new-key" {
		t.Fatal(all)
	}
}
func TestSessionServiceRootChangeRequiresRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setting, e := NewSettingService(filepath.Join(home, "setting.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	files, _ := store.Open(setting.Get().Session.WorkRoot)
	sessions := SessionService{Files: files, Settings: setting}
	cfg := setting.Get()
	cfg.Session.WorkRoot = filepath.Join(home, "new-root")
	if e = setting.Save(cfg); e != nil {
		t.Fatal(e)
	}
	if _, _, e = sessions.Acquire(context.Background(), "ep", "c", "a", "codex", "v"); e == nil {
		t.Fatal("mixed roots allowed without restart")
	}
}

func TestUnknownServerNeverFallsBackToAnotherEndpointsKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agentuse", "endpoints.yaml")
	if e := (EndpointService{Path: path}).Save(Endpoint{ServerURL: "https://one.example", Enabled: true, DeviceID: "d1", DeviceKey: "key1"}); e != nil {
		t.Fatal(e)
	}
	t.Setenv("AGENTUSE_SERVER", "https://other.example")
	if c, e := LoadConfig(path); e == nil {
		t.Fatal("credential fallback to another endpoint", c.Server)
	}
	if _, e := LoadConfigForServer(path, "https://other.example"); e == nil {
		t.Fatal("unknown endpoint accepted")
	}
}
