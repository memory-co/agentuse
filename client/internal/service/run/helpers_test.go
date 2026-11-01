package run

import (
	"os"
	"path/filepath"
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
