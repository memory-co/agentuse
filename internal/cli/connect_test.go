package cli

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/hub"
	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/internal/services/token"
	"github.com/memory-co/agentuse/internal/store"
	"github.com/memory-co/agentuse/pkg/sdk"
)

// agentuse connect:在仓库子目录里跑,兑换接入码,写 0600 的 daemon.json;第二次接入复用设备。
func TestConnect(t *testing.T) {
	ctx := context.Background()
	h := hub.New([]models.Platform{{ID: "shop", Secret: "s", Policy: models.Policy{Actions: []models.Action{models.ActionReply}}}},
		store.NewMemory(), token.NewIssuer([]byte("k")), nil, hub.Options{})
	srv := httptest.NewServer(hub.NewServer(h, hub.ServerConfig{}).Handler())
	defer srv.Close()
	platform := sdk.New(srv.URL, "shop", "s")

	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "trunk"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	sub := filepath.Join(repo, "pkg")
	os.Mkdir(sub, 0o755)
	cfgPath := filepath.Join(t.TempDir(), "daemon.json")

	run := func(args ...string) error {
		return connect(append(args, "-config", cfgPath, "-no-check", "-name", "alice-mbp"))
	}
	if err := run(srv.URL, "ac_nope", "-dir", sub); err == nil {
		t.Fatal("bad code accepted")
	}
	if err := run(srv.URL, "ac_x", "-dir", t.TempDir()); err == nil {
		t.Fatal("non-repo dir accepted")
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("daemon.json written on failure: %v", err)
	}

	c, err := platform.CreateAgent(ctx, sdk.NewAgent{Owner: "alice", Handle: "alice-claude", AgentUserID: "app_a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(srv.URL+"/", c.Connect.Code, "-dir", sub); err != nil {
		t.Fatalf("connect: %v", err)
	}
	st, err := os.Stat(cfgPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("daemon.json: %v %v", st, err)
	}
	cfg, err := config.LoadDaemon(cfgPath)
	if err != nil || len(cfg.Hubs) != 1 || cfg.Hubs[0].URL != srv.URL || cfg.Hubs[0].Platform != "shop" {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	a, _ := platform.Agent(ctx, c.Agent.ID)
	top, _ := filepath.EvalSymlinks(repo)
	if got, _ := filepath.EvalSymlinks(a.Workdir); got != top || a.BaseRef != "trunk" || a.Device.Name != "alice-mbp" {
		t.Fatalf("agent bound to the repo root on the current branch: %+v", a)
	}

	// 同一台电脑接第二个 agent:还是一个设备、一条配置。
	c2, _ := platform.CreateAgent(ctx, sdk.NewAgent{Owner: "alice", Handle: "alice-review", AgentUserID: "app_r"})
	if err := run(srv.URL, c2.Connect.Code, "-dir", repo, "-base-ref", "trunk"); err != nil {
		t.Fatal(err)
	}
	cfg2, _ := config.LoadDaemon(cfgPath)
	if len(cfg2.Hubs) != 1 || cfg2.Hubs[0] != cfg.Hubs[0] {
		t.Fatalf("second agent should reuse the device: %+v", cfg2)
	}
	if a2, _ := platform.Agent(ctx, c2.Agent.ID); a2.DeviceID != a.DeviceID {
		t.Fatalf("second agent on another device: %+v", a2)
	}

	if err := disconnect([]string{"@alice-review", "-config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if a2, _ := platform.Agent(ctx, c2.Agent.ID); a2.Connected() {
		t.Fatalf("still connected: %+v", a2)
	}
}
