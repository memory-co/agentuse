package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/memory-co/agentuse/client/internal/store"
	bundled "github.com/memory-co/agentuse/harnessctl"
)

type Service struct{ Root string }
type Manifest struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Protocol   string   `json:"protocol"`
	Entrypoint string   `json:"entrypoint"`
	Platforms  []string `json:"platforms"`
	Requires   struct {
		Python string   `json:"python"`
		Bin    []string `json:"bin"`
	} `json:"requires"`
	Commands []string `json:"commands"`
	Requests []string `json:"requests"`
}
type Adapter struct {
	Name, Path, Revision, Version, PackageDir, HistoryPath, EventsPath string
	Manifest                                                           Manifest
	Disabled                                                           bool
	Env                                                                []string
}

func (a Adapter) CanSteer() bool { return true } // send owns native insertion/queue policy
func (a Adapter) CanAsk() bool   { return len(a.Manifest.Requests) > 0 }

var packagesMu sync.Mutex

// AgentUse's registry key remains claude; the independently named package is claudecode.
var builtinPackages = map[string]string{"claude": "claudecode", "codex": "codex"}

func HarnessRoot(cfg Config) string {
	if cfg.ConfigPath != "" {
		return filepath.Join(filepath.Dir(cfg.ConfigPath), "harness")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "harness")
}
func revision(root string) (string, error) {
	sum := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("adapter symlink not allowed: %s", path)
		}
		rel, _ := filepath.Rel(root, path)
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sum.Write([]byte(filepath.ToSlash(rel)))
		sum.Write([]byte{0})
		sum.Write(data)
		return nil
	})
	return hex.EncodeToString(sum.Sum(nil))[:16], err
}
func (s Service) installBuiltins() (map[string]string, error) {
	installed := map[string]string{}
	for name, source := range builtinPackages {
		base := "harness/" + source
		sum := sha256.New()
		e := fs.WalkDir(bundled.Files, base, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() {
				b, e := bundled.Files.ReadFile(path)
				if e != nil {
					return e
				}
				sum.Write([]byte(strings.TrimPrefix(path, base+"/")))
				sum.Write(b)
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
		id := hex.EncodeToString(sum.Sum(nil))[:16]
		installed[name] = id
		target, e := store.SafePath(s.Root, name, id)
		if e != nil {
			return nil, e
		}
		if _, e = os.Stat(filepath.Join(target, "manifest.json")); e == nil {
			continue
		}
		e = fs.WalkDir(bundled.Files, base, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(base, path)
			out := filepath.Join(target, rel)
			if d.IsDir() {
				return os.MkdirAll(out, 0700)
			}
			b, e := bundled.Files.ReadFile(path)
			if e != nil {
				return e
			}
			mode := os.FileMode(0600)
			if d.Name() == "harnessctl" {
				mode = 0700
			}
			return os.WriteFile(out, b, mode)
		})
		if e != nil {
			return nil, e
		}
	}
	return installed, nil
}
func (s Service) Packages() (map[string]Adapter, error) {
	packagesMu.Lock()
	defer packagesMu.Unlock()
	builtins, e := s.installBuiltins()
	if e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(s.Root)
	if e != nil {
		return nil, e
	}
	out := map[string]Adapter{}
	for _, entry := range entries {
		if !entry.IsDir() || !store.Segment(entry.Name()) {
			continue
		}
		name := entry.Name()
		parent, e := store.SafePath(s.Root, name)
		if e != nil {
			return nil, e
		}
		versions, e := os.ReadDir(parent)
		if e != nil {
			return nil, e
		}
		names := []string{}
		for _, v := range versions {
			if v.IsDir() {
				names = append(names, v.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		ordered := []string{"local"}
		if builtins[name] != "" {
			ordered = append(ordered, builtins[name])
		}
		ordered = append(ordered, names...)
		for _, v := range ordered {
			dir, e := store.SafePath(parent, v)
			if e != nil {
				return nil, e
			}
			data, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return nil, e
			}
			var m Manifest
			if e = json.Unmarshal(data, &m); e != nil {
				return nil, e
			}
			if m.Protocol != "harnessctl/2" || m.Entrypoint == "" || filepath.Base(m.Entrypoint) != m.Entrypoint {
				return nil, fmt.Errorf("invalid harnessctl package %s", dir)
			}
			disabled := false
			if b, e := os.ReadFile(filepath.Join(parent, "local", "disabled")); e == nil {
				disabled = strings.TrimSpace(string(b)) != "false"
			}
			if b, e := os.ReadFile(filepath.Join(dir, "disabled")); e == nil {
				disabled = strings.TrimSpace(string(b)) != "false"
			}
			rev, e := revision(dir)
			if e != nil {
				return nil, e
			}
			path, e := store.SafePath(dir, m.Entrypoint)
			if e != nil {
				return nil, e
			}
			out[name] = Adapter{Name: name, Path: path, PackageDir: dir, Revision: rev, Version: m.Version, Manifest: m, Disabled: disabled}
			break
		}
	}
	return out, nil
}

// SaveCommandAdapters installs generic harnessctl executables; it never describes native protocols.
func SaveCommandAdapters(root string, commands map[string]HarnessConfig) error {
	for name, c := range commands {
		dir, e := store.SafePath(root, name, "local")
		if e != nil {
			return e
		}
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		if c.Command == "" {
			if c.Disabled {
				if e = os.WriteFile(filepath.Join(dir, "disabled"), []byte("true"), 0600); e != nil {
					return e
				}
			}
			continue
		}
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
		parts := []string{quote(c.Command)}
		for _, a := range c.Args {
			parts = append(parts, quote(a))
		}
		if e = os.WriteFile(filepath.Join(dir, "harnessctl"), []byte("#!/bin/sh\nexec "+strings.Join(parts, " ")+" \"$@\"\n"), 0700); e != nil {
			return e
		}
		m := map[string]any{"name": name, "version": "local", "protocol": "harnessctl/2", "entrypoint": "harnessctl", "commands": []string{"send", "cancel", "status", "reply"}, "requests": []string{"permission", "question"}}
		b, _ := json.Marshal(m)
		if e = os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); e != nil {
			return e
		}
		if c.Disabled {
			if e = os.WriteFile(filepath.Join(dir, "disabled"), []byte("true"), 0600); e != nil {
				return e
			}
		} else if e = os.Remove(filepath.Join(dir, "disabled")); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}
