package setting

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsFilesValidateAndRetainLastValidSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agentuse", "setting.yaml")
	s, e := New(path)
	if e != nil {
		t.Fatal(e)
	}
	v := s.Get()
	if v.LocalHTTP.Port != 7801 || v.Session.Quota != nil {
		t.Fatal(v)
	}
	v.Session.Quota = &Quota{MaxBytes: 0}
	if e = s.Save(v); e == nil {
		t.Fatal("invalid quota accepted")
	}
	if s.Get().Session.Quota != nil {
		t.Fatal("invalid setting applied")
	}
	os.WriteFile(path, []byte("execution:\n  max_running_runs: 1\n"), 0600)
	if e = s.Reload(); e == nil {
		t.Fatal("obsolete setting accepted")
	}
	if s.Get().LocalHTTP.Port != 7801 {
		t.Fatal("last valid setting lost")
	}
	v = Defaults()
	for _, root := range []string{home, "/", filepath.Join(home, ".agentuse", "harness"), filepath.Join(home, ".claude", "sessions")} {
		v.Session.WorkRoot = root
		if e = v.Validate(); e == nil {
			t.Fatal("unsafe root accepted", root)
		}
	}
}
