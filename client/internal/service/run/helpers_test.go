package run

import (
	"fmt"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fakeBin(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if e := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); e != nil {
		t.Fatal(e)
	}
	return path
}

func fixtureCommand(t *testing.T, backend string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	script, err := filepath.Abs(filepath.Join(filepath.Dir(file), "../../../../harnessctl/tests/fake_adapter.py"))
	if err != nil {
		t.Fatal(err)
	}
	return fakeBin(t, t.TempDir(), "harnessctl", fmt.Sprintf("exec python3 %q %q \"$@\"", script, backend))
}
func installDebugHarness(t *testing.T, home, script string) {
	t.Helper()
	backend := fakeBin(t, home, "debug-worker", script)
	command := fixtureCommand(t, backend)
	if err := harness.SaveCommandAdapters(filepath.Join(home, ".agentuse", "harness"), map[string]harness.HarnessConfig{"debug": {Command: command}}); err != nil {
		t.Fatal(err)
	}
}
