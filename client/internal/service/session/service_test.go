package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/memory-co/agentuse/client/internal/service/setting"
)

func TestSessionServiceRootChangeRequiresRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setting, e := setting.New(filepath.Join(home, "setting.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	files, _ := New(setting.Get().Session.WorkRoot)
	sessions := files
	sessions.Settings = setting
	cfg := setting.Get()
	cfg.Session.WorkRoot = filepath.Join(home, "new-root")
	if e = setting.Save(cfg); e != nil {
		t.Fatal(e)
	}
	if _, _, e = sessions.Acquire(context.Background(), "ep", "c", "a", "codex", "v", "agent-version"); e != nil {
		t.Fatal("running session configuration changed before restart", e)
	}
}
