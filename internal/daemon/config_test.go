package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "daemon.json")
	if _, err := LoadConfig(path); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	c := Config{Server: "http://x", DaemonID: "dm_1", Key: "dk_1", Workdir: "~/w",
		Harnesses: map[string]HarnessConfig{"mine": {Command: "/bin/mine", Args: []string{"-v"}}, "codex": {Disabled: true}}}
	if err := SaveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("密钥文件权限应为 0600: %v", st.Mode())
	}
	got, err := LoadConfig(path)
	if err != nil || !reflect.DeepEqual(got, c) {
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
	path := filepath.Join(t.TempDir(), "daemon.json")
	// 已有的 harnesses 配置注册后保留
	SaveConfig(path, Config{Harnesses: map[string]HarnessConfig{"mine": {Command: "m"}}})

	cfg, err := Register(ctx, ts.URL+"/", "st_ok", path)
	if err != nil || cfg.Server != ts.URL || cfg.DaemonID != "dm_1" || cfg.Key != "dk_1" || cfg.Harnesses["mine"].Command != "m" {
		t.Fatalf("register: %v %+v", err, cfg)
	}
	if saved, _ := LoadConfig(path); !reflect.DeepEqual(saved, cfg) {
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
