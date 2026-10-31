package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
)

func TestCheckKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer dk_ok":
			json.NewEncoder(w).Encode(protocol.MeResponse{DaemonID: "dm_1", Status: "online"})
		case "Bearer dk_500":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	if me, err := CheckKey(ctx, Config{Server: ts.URL + "/", Key: "dk_ok"}); err != nil || me.DaemonID != "dm_1" {
		t.Fatalf("ok: %v %+v", err, me)
	}
	if _, err := CheckKey(ctx, Config{Server: ts.URL, Key: "dk_bad"}); !errors.Is(err, errUnauthorized) {
		t.Fatalf("401: %v", err)
	}
	if _, err := CheckKey(ctx, Config{Server: ts.URL, Key: "dk_500"}); err == nil || errors.Is(err, errUnauthorized) {
		t.Fatalf("500: %v", err)
	}
}

// fakeBin 在 dir 里放一个可执行脚本。
func fakeBin(t *testing.T, dir, name, script string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListHarnesses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // PATH 里只有 claude,没有 codex
	fakeBin(t, dir, "claude", `echo "2.0.1 (Claude Code)"; echo second line`)
	mine := fakeBin(t, t.TempDir(), "mine", "exit 0")

	got := ListHarnesses(Config{Harnesses: map[string]HarnessConfig{
		"mine":  {Command: mine, Args: []string{"-x"}},
		"ghost": {Command: "/nonexistent/ghost"},
		"off":   {Command: mine, Disabled: true},
	}})
	want := []HarnessInfo{
		{Name: "claude", Path: filepath.Join(dir, "claude"), Version: "2.0.1 (Claude Code)", Found: true},
		{Name: "codex"},
		{Name: "ghost"},
		{Name: "mine", Path: mine, Found: true}, // 自定义 harness 不探测版本
		{Name: "off", Disabled: true},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}

	// 内置 harness 可以在配置里换命令、带参数，也可以禁用
	found := detectHarnesses(Config{Harnesses: map[string]HarnessConfig{
		"codex":  {Command: filepath.Join(dir, "claude"), Args: []string{"-s"}},
		"claude": {Disabled: true},
	}})
	if _, ok := found["claude"]; ok || found["codex"].Spec == nil || found["codex"].Args[0] != "-s" || len(found) != 1 {
		t.Fatalf("override: %+v", found)
	}
}

func TestServiceStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // 没有 systemctl / launchctl
	if got := ServiceStatus(); got != "未检测到" {
		t.Fatalf("nothing: %q", got)
	}
	os.MkdirAll(filepath.Join(home, ".agentuse"), 0o700)
	os.WriteFile(filepath.Join(home, ".agentuse", "daemon.pid"), []byte("\n"+strconv.Itoa(os.Getpid())+"\n"), 0o600)
	if got := ServiceStatus(); got != "nohup: 运行中(pid "+strconv.Itoa(os.Getpid())+")" {
		t.Fatalf("pidfile: %q", got)
	}
}
