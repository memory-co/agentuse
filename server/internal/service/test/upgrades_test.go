package test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	upgradeservice "github.com/memory-co/agentuse/server/internal/service/upgrade"
)

type upgradeFixture struct {
	config   upgradeservice.Config
	service  *upgradeservice.Service
	files    map[string][]byte
	restarts chan upgradeservice.Restart
}

func newUpgradeFixture(t *testing.T, target string, corrupt bool, gate <-chan struct{}) *upgradeFixture {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("binary upgrades support Linux/macOS")
	}
	s := newService(t)
	root := t.TempDir()
	f := &upgradeFixture{files: map[string][]byte{}, restarts: make(chan upgradeservice.Restart, 2)}
	names := []string{"agentuse-linux-amd64", "agentuse-linux-arm64", "agentuse-darwin-amd64", "agentuse-darwin-arm64", "agentuse-server-" + runtime.GOOS + "-" + runtime.GOARCH}
	for _, name := range names {
		f.files[name] = []byte("new:" + name)
	}
	f.files[names[4]] = []byte("#!/bin/sh\necho " + target + "\n")
	var sums strings.Builder
	var assets []map[string]any
	for _, name := range names {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(f.files[name]), name)
		assets = append(assets, map[string]any{"name": name, "size": len(f.files[name])})
	}
	if corrupt {
		f.files[names[0]][0] = '!'
	}
	f.files["SHA256SUMS"] = []byte(sums.String())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			if gate != nil {
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": target, "html_url": "https://github.com/memory-co/agentuse/releases/tag/" + target, "assets": assets})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/download/"+target+"/")
		data, ok := f.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(server.Close)
	f.config = upgradeservice.Config{Root: filepath.Join(root, "upgrades"), Executable: filepath.Join(root, "agentuse-server"), DistDir: filepath.Join(root, "dist"), Instance: "test", Version: "v9.0.0", APIURL: server.URL + "/latest", DownloadURL: server.URL + "/download", Restart: func(_ context.Context, plan upgradeservice.Restart) error { f.restarts <- plan; return nil }}
	if err := os.MkdirAll(f.config.DistDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.config.Executable, []byte("old-server"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names[:4] {
		if err := os.WriteFile(filepath.Join(f.config.DistDir, name), []byte("old:"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.service = upgradeservice.New(s.DB, f.config)
	t.Cleanup(f.service.Close)
	return f
}

func waitUpgrade(t *testing.T, s *upgradeservice.Service, id, status string) agentuse.ServerUpgrade {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		value, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if value.Status == status {
			return value
		}
		if value.Status == "failed" && status != "failed" {
			t.Fatalf("upgrade failed: %s", value.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("upgrade did not reach %s", status)
	return agentuse.ServerUpgrade{}
}

func TestUpgradeAsyncDeduplicatesAndConfirmsNewStartup(t *testing.T) {
	gate := make(chan struct{})
	f := newUpgradeFixture(t, "v9.1.0", false, gate)
	ctx, cancel := context.WithCancel(context.Background())
	first, err := f.service.Start(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Work must survive the initiating HTTP request.
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			next, e := f.service.Start(context.Background(), "admin")
			if e != nil || next.ID != first.ID {
				t.Errorf("duplicate request: %v %+v", e, next)
			}
		}()
	}
	group.Wait()
	close(gate)
	value := waitUpgrade(t, f.service, first.ID, "restarting")
	if value.CompletedAt != nil || value.DownloadedBytes != value.TotalBytes || value.TotalBytes == 0 {
		t.Fatal("restart is not confirmation", value)
	}
	select {
	case <-f.restarts:
	case <-time.After(time.Second):
		t.Fatal("restart was not requested")
	}
	for name, expected := range f.files {
		if name == "SHA256SUMS" {
			continue
		}
		path := filepath.Join(f.config.DistDir, name)
		if strings.HasPrefix(name, "agentuse-server-") {
			path = f.config.Executable
		}
		actual, e := os.ReadFile(path)
		if e != nil || string(actual) != string(expected) {
			t.Fatal("replacement mismatch", name, e)
		}
	}
	f.service.Close()
	f.config.Version = "v9.1.0"
	newProcess := upgradeservice.New(f.service.DB, f.config)
	defer newProcess.Close()
	if err = newProcess.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	value = waitUpgrade(t, newProcess, first.ID, "succeeded")
	state, err := newProcess.State(context.Background(), "", 20)
	if err != nil || state.Active != nil || len(state.Upgrades) != 1 || value.CompletedAt == nil || state.CurrentVersion != "v9.1.0" {
		t.Fatal(state, err)
	}
}

func assertOriginalUpgradeFiles(t *testing.T, f *upgradeFixture) {
	t.Helper()
	actual, err := os.ReadFile(f.config.Executable)
	if err != nil || string(actual) != "old-server" {
		t.Fatal("original Server changed", err)
	}
	for _, name := range []string{"agentuse-linux-amd64", "agentuse-linux-arm64", "agentuse-darwin-amd64", "agentuse-darwin-arm64"} {
		actual, err = os.ReadFile(filepath.Join(f.config.DistDir, name))
		if err != nil || string(actual) != "old:"+name {
			t.Fatal("original client changed", name, err)
		}
	}
}

func TestUpgradeChecksumFailureAndHistoryPagination(t *testing.T) {
	f := newUpgradeFixture(t, "v9.1.0", true, nil)
	var ids []string
	for range 2 {
		item, err := f.service.Start(context.Background(), "admin")
		if err != nil {
			t.Fatal(err)
		}
		item = waitUpgrade(t, f.service, item.ID, "failed")
		if !strings.Contains(item.Error, "SHA256") || item.CompletedAt == nil {
			t.Fatal(item)
		}
		ids = append(ids, item.ID)
	}
	assertOriginalUpgradeFiles(t, f)
	select {
	case <-f.restarts:
		t.Fatal("corrupt files must not restart")
	default:
	}
	page, err := f.service.State(context.Background(), "", 1)
	if err != nil || len(page.Upgrades) != 1 || page.Upgrades[0].ID != ids[1] || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	page, err = f.service.State(context.Background(), *page.NextCursor, 1)
	if err != nil || len(page.Upgrades) != 1 || page.Upgrades[0].ID != ids[0] || page.NextCursor != nil {
		t.Fatal(page, err)
	}
}

func TestUpgradeRestartFailureRestoresEveryFile(t *testing.T) {
	f := newUpgradeFixture(t, "v9.1.0", false, nil)
	f.service.Close()
	f.config.Restart = func(context.Context, upgradeservice.Restart) error { return errors.New("restart refused") }
	s := upgradeservice.New(f.service.DB, f.config)
	defer s.Close()
	item, err := s.Start(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	item = waitUpgrade(t, s, item.ID, "failed")
	if !strings.Contains(item.Error, "restart refused") {
		t.Fatal(item)
	}
	assertOriginalUpgradeFiles(t, f)
}

func TestUpgradeInterruptedHandoffRestoresOldVersion(t *testing.T) {
	f := newUpgradeFixture(t, "v9.1.0", false, nil)
	item, err := f.service.Start(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, f.service, item.ID, "restarting")
	f.service.Close()
	oldProcess := upgradeservice.New(f.service.DB, f.config)
	defer oldProcess.Close()
	if err = oldProcess.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, oldProcess, item.ID, "failed")
	assertOriginalUpgradeFiles(t, f)
}

func TestUpgradeDoesNotDowngrade(t *testing.T) {
	f := newUpgradeFixture(t, "v8.9.9", false, nil)
	item, err := f.service.Start(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, f.service, item.ID, "unchanged")
	assertOriginalUpgradeFiles(t, f)
}

func TestUpgradeRecoversFinalReplacementCheckpoint(t *testing.T) {
	f := newUpgradeFixture(t, "v9.1.0", false, nil)
	item, err := f.service.Start(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, f.service, item.ID, "restarting")
	f.service.Close()
	if err = f.service.DB.Model(&database.ServerUpgrade{}).Where("id=?", item.ID).Update("status", "installing").Error; err != nil {
		t.Fatal(err)
	}
	f.config.Version = "v9.1.0"
	next := upgradeservice.New(f.service.DB, f.config)
	defer next.Close()
	if err = next.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, next, item.ID, "succeeded")
}
