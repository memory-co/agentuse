package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// InstallBuiltins materializes bundled adapters once. Subsequent execution loads
// disk files; user edits and additional packages remain local to the client.
func InstallBuiltins(root string) error {
	all, e := All()
	if e != nil {
		return e
	}
	for _, sp := range all {
		sum := sha256.New()
		e = fs.WalkDir(files, sp.Name, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			b, err := files.ReadFile(p)
			if err != nil {
				return err
			}
			sum.Write([]byte(p))
			sum.Write(b)
			return nil
		})
		if e != nil {
			return e
		}
		revision := hex.EncodeToString(sum.Sum(nil))[:16]
		dst := filepath.Join(root, sp.Name, revision)
		if _, e = os.Stat(filepath.Join(dst, "harness.yaml")); e == nil {
			continue
		}
		if e = copyTree(sp.Name, dst); e != nil {
			return e
		}
	}
	return nil
}
func LoadLocal(dir string) (Spec, error) {
	var s Spec
	b, e := os.ReadFile(filepath.Join(dir, "harness.yaml"))
	if e != nil {
		return s, e
	}
	if e = yaml.Unmarshal(b, &s); e != nil {
		return s, e
	}
	if e = s.validate(os.DirFS(dir), "."); e != nil {
		return s, e
	}
	return s, nil
}
func Revision(dir string) (string, error) {
	sum := sha256.New()
	e := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("adapter symlink not allowed")
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		sum.Write([]byte(filepath.ToSlash(rel)))
		sum.Write([]byte{0})
		sum.Write(b)
		return nil
	})
	return hex.EncodeToString(sum.Sum(nil))[:16], e
}
func SkillInstructions(dir string) (string, error) {
	var out strings.Builder
	root := filepath.Join(dir, "skills")
	if _, e := os.Stat(root); os.IsNotExist(e) {
		return "", nil
	}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill symlink not allowed")
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if len(b) > 64<<10 {
			return fmt.Errorf("skill too large")
		}
		out.WriteString("\n\n")
		out.Write(b)
		return nil
	})
	return out.String(), e
}

// CopyBuiltin seeds a local adapter package, including its resource files.
func CopyBuiltin(name, destination string) error {
	if _, ok := Get(name); !ok {
		return fmt.Errorf("unknown builtin adapter %s", name)
	}
	return copyTree(name, destination)
}
