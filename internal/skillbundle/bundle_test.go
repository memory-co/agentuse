package skillbundle

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func archive(t *testing.T, names, bodies []string, mode os.FileMode) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		h.SetMode(mode)
		f, e := w.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write([]byte(bodies[i])); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestSkillArchiveValidationAndExtraction(t *testing.T) {
	manifest := "---\nname: useful\ndescription: Useful tools\n---\nUse this skill."
	good := archive(t, []string{"SKILL.md", "scripts/check.sh"}, []string{manifest, "#!/bin/sh\ntrue\n"}, 0700)
	dir := t.TempDir()
	meta, e := Extract(good, dir)
	if e != nil || meta.Name != "useful" {
		t.Fatal(meta, e)
	}
	info, e := os.Stat(filepath.Join(dir, "scripts/check.sh"))
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, e)
	}
	cases := []struct {
		name          string
		names, bodies []string
		mode          os.FileMode
	}{
		{"file-as-parent", []string{"SKILL.md", "scripts", "scripts/a"}, []string{manifest, "file", "bad"}, 0600},
		{"parent-as-file", []string{"SKILL.md", "scripts/a", "scripts"}, []string{manifest, "bad", "file"}, 0600},
		{"dot", []string{"SKILL.md", "."}, []string{manifest, "bad"}, 0600},
		{"traversal", []string{"SKILL.md", "../escaped"}, []string{manifest, "bad"}, 0600},
		{"absolute", []string{"/SKILL.md"}, []string{manifest}, 0600},
		{"backslash", []string{"SKILL.md", "a\\b"}, []string{manifest, "bad"}, 0600},
		{"case-collision", []string{"SKILL.md", "skill.md"}, []string{manifest, manifest}, 0600},
		{"symlink", []string{"SKILL.md"}, []string{manifest}, os.ModeSymlink | 0777},
		{"plugin", []string{"SKILL.md", ".claude-plugin/plugin.json"}, []string{manifest, "{}"}, 0600},
		{"no-root-manifest", []string{"useful/SKILL.md"}, []string{manifest}, 0600},
		{"typed-name", []string{"SKILL.md"}, []string{"---\nname: true\ndescription: tools\n---\n"}, 0600},
		{"missing-description", []string{"SKILL.md"}, []string{"---\nname: useful\n---\n"}, 0600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, e := Inspect(archive(t, c.names, c.bodies, c.mode)); e == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	if _, e := Inspect([]byte("not ZIP")); e == nil {
		t.Fatal("invalid ZIP accepted")
	}
}
