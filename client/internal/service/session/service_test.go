package session

import (
	"context"
	"github.com/memory-co/agentuse/client/internal/service/setting"
	"github.com/memory-co/agentuse/client/internal/store"
	"path/filepath"
	"testing"
)

func TestSessionServiceRootChangeRequiresRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setting, e := setting.New(filepath.Join(home, "setting.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	files, _ := store.Open(setting.Get().Session.WorkRoot)
	sessions := Service{Files: files, Settings: setting}
	cfg := setting.Get()
	cfg.Session.WorkRoot = filepath.Join(home, "new-root")
	if e = setting.Save(cfg); e != nil {
		t.Fatal(e)
	}
	if _, _, e = sessions.Acquire(context.Background(), "ep", "c", "a", "codex", "v"); e == nil {
		t.Fatal("mixed roots allowed without restart")
	}
}
