package service

import (
	"context"
	"encoding/json"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "endpoints.yaml")
	if _, err := LoadConfig(path); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	c := run.Config{Server: "http://x", DaemonID: "dm_1", Key: "dk_1",
		Harnesses: map[string]harness.HarnessConfig{"mine": {Command: "/bin/mine", Args: []string{"-v"}}, "codex": {Disabled: true}}}
	if err := saveExecutionConfig(path, c); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("密钥文件权限应为 0600: %v", st.Mode())
	}
	c.ConfigPath = path
	got, err := LoadConfig(path)
	if err != nil || got.Server != c.Server || got.DaemonID != c.DaemonID || got.Key != c.Key || got.EndpointID != "http---x" {
		t.Fatalf("%v %+v", err, got)
	}
	os.WriteFile(path, []byte("{"), 0o600)
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("bad json: %v", err)
	}
}

func TestRegister(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.RegisterRequest
		json.NewDecoder(r.Body).Decode(&req)
		switch {
		case r.URL.Path != protocol.RegisterPath || r.Method != http.MethodPost:
			http.NotFound(w, r)
		case req.Token == "st_ok" && req.Hostname != "":
			json.NewEncoder(w).Encode(protocol.RegisterResponse{DaemonID: "dm_1", Name: req.Hostname, Key: "dk_1"})
		case req.Token == "st_text":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "安装令牌无效"})
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "endpoints.yaml")
	// 已有的 harnesses 配置注册后保留
	saveExecutionConfig(path, run.Config{Harnesses: map[string]harness.HarnessConfig{"mine": {Command: "m"}}})

	cfg, err := Register(ctx, ts.URL+"/", "st_ok", path)
	if err != nil || cfg.Server != ts.URL || cfg.DaemonID != "dm_1" || cfg.Key != "dk_1" {
		t.Fatalf("register: %v %+v", err, cfg)
	}
	if saved, _ := LoadConfig(path); saved.Server != cfg.Server || saved.Key != cfg.Key {
		t.Fatalf("saved: %+v", saved)
	}
	if _, err := Register(ctx, ts.URL, "st_bad", path); err == nil || err.Error() != "安装令牌无效" {
		t.Fatalf("server error message: %v", err)
	}
	if _, err := Register(ctx, ts.URL, "st_text", path); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("non-JSON error: %v", err)
	}
	if _, err := Register(ctx, "http://127.0.0.1:1", "st_ok", path); err == nil {
		t.Fatal("unreachable server should fail")
	}
}

func saveExecutionConfig(path string, c run.Config) error {
	if e := harness.SaveCommandAdapters(filepath.Join(filepath.Dir(path), "harness"), c.Harnesses); e != nil {
		return e
	}
	if c.Server == "" {
		return nil
	}
	return (endpoint.Service{Path: path}).Save(endpoint.Endpoint{Name: c.Server, ServerURL: c.Server, Enabled: true, DeviceID: c.DaemonID, DeviceKey: c.Key})
}

func TestUnknownServerNeverFallsBackToAnotherEndpointsKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agentuse", "endpoints.yaml")
	if e := (endpoint.Service{Path: path}).Save(endpoint.Endpoint{ServerURL: "https://one.example", Enabled: true, DeviceID: "d1", DeviceKey: "key1"}); e != nil {
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

func TestRunningClientUsesStartupConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agentuse", "endpoints.yaml")
	root := filepath.Join(home, ".agentuse", "tmp")
	files, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	adapterRoot := filepath.Join(filepath.Dir(path), "harness")
	if err = harness.SaveCommandAdapters(adapterRoot, map[string]harness.HarnessConfig{"custom": {Command: "/bin/true"}}); err != nil {
		t.Fatal(err)
	}
	manager, err := New(path, files)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	before, err := manager.Harness.Resolve("custom")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(before.Path)
	if err != nil {
		t.Fatal(err)
	}
	config := manager.Settings.Get()
	config.LocalHTTP.Port = 8099
	if err = manager.Settings.Save(config); err != nil {
		t.Fatal(err)
	}
	writer := endpoint.Service{Path: path}
	if err = writer.Save(endpoint.Endpoint{Name: "new", ServerURL: "https://new.example", Enabled: true, DeviceID: "device", DeviceKey: "key"}); err != nil {
		t.Fatal(err)
	}
	if err = harness.SaveCommandAdapters(adapterRoot, map[string]harness.HarnessConfig{"custom": {Command: "/bin/false"}, "added": {Command: "/bin/true"}}); err != nil {
		t.Fatal(err)
	}
	if manager.Settings.Get().LocalHTTP.Port == 8099 {
		t.Fatal("settings changed without restart")
	}
	endpoints, err := manager.Endpoints.List()
	if err != nil || len(endpoints) != 1 {
		t.Fatal("endpoint appeared without restart", endpoints, err)
	}
	current, err := manager.Harness.Resolve("custom")
	if err != nil || current.Revision != before.Revision {
		t.Fatal("adapter changed without restart", err)
	}
	data, err := os.ReadFile(current.Path)
	if err != nil || string(data) != string(original) {
		t.Fatal("running executable changed", err)
	}
	if _, err = manager.Harness.Resolve("added"); err == nil {
		t.Fatal("new adapter appeared without restart")
	}
	restarted, err := New(path, files)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.Settings.Get().LocalHTTP.Port != 8099 {
		t.Fatal("restart did not load settings")
	}
	endpoints, err = restarted.Endpoints.List()
	if err != nil || len(endpoints) != 2 {
		t.Fatal(endpoints, err)
	}
	updated, err := restarted.Harness.Resolve("custom")
	if err != nil || updated.Revision == before.Revision {
		t.Fatal("restart did not load adapter", err)
	}
	if _, err = restarted.Harness.Resolve("added"); err != nil {
		t.Fatal(err)
	}
}
