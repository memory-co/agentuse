package file

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalNamespacesAtomicAndPaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := NewLocal(root)
	if e != nil {
		t.Fatal(e)
	}
	for _, ns := range []string{"skills", "conversations"} {
		if e = s.Put(ctx, ns, "a/v/package", strings.NewReader(ns), true); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Put(ctx, "skills", "a/v/package", strings.NewReader("changed"), true); !errors.Is(e, ErrExists) {
		t.Fatal(e)
	}
	r, n, e := s.Get(ctx, "skills", "a/v/package")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "skills" || n != 6 {
		t.Fatal(string(b), n)
	}
	rows, e := s.List(ctx, "skills", "")
	if e != nil || len(rows) != 1 || rows[0].Path != "a/v/package" {
		t.Fatal(rows, e)
	}
	if e = s.DeletePrefix(ctx, "skills", "a"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Get(ctx, "skills", "a/v/package"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if _, _, e = s.Get(ctx, "conversations", "a/v/package"); e != nil {
		t.Fatal("cross namespace deletion", e)
	}
	for _, p := range []string{"../escape", "a/../../escape", "/absolute", "a\\b", "a/./b", "a//b"} {
		if e = s.Put(ctx, "skills", p, strings.NewReader("bad"), false); e == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	outside := t.TempDir()
	if e = os.Symlink(outside, filepath.Join(root, "skills", "link")); e != nil {
		t.Fatal(e)
	}
	if e = s.Put(ctx, "skills", "link/escape", strings.NewReader("bad"), false); e == nil {
		t.Fatal("symlink escaped storage root")
	}
	if _, e = os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(e) {
		t.Fatal("outside file written", e)
	}
}
