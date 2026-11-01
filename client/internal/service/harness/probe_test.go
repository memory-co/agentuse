package harness

import (
	"os"
	"path/filepath"
	"testing"
)

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
	t.Setenv("HOME", t.TempDir())
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
		got[i].Revision = ""
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}

	// 内置 harness 可以在配置里换命令、带参数，也可以禁用
	found := Detect(Config{Harnesses: map[string]HarnessConfig{
		"codex":  {Command: filepath.Join(dir, "claude"), Args: []string{"-s"}},
		"claude": {Disabled: true},
	}})
	if _, ok := found["claude"]; ok || found["codex"].Spec == nil || found["codex"].Args[0] != "-s" || len(found) != 1 {
		t.Fatalf("override: %+v", found)
	}
}
