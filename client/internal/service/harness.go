package service

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/harness"
	"gopkg.in/yaml.v3"
)

// HarnessService owns locally installed adapters, never server configuration.
type HarnessService struct{ Root string }

func HarnessRoot(cfg Config) string {
	if cfg.ConfigPath != "" {
		return filepath.Join(filepath.Dir(cfg.ConfigPath), "harness")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "harness")
}
func (s HarnessService) Packages() (map[string]localHarness, error) {
	if e := harness.InstallBuiltins(s.Root); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(s.Root)
	if e != nil {
		return nil, e
	}
	out := map[string]localHarness{}
	for _, entry := range entries {
		if !entry.IsDir() || !store.Segment(entry.Name()) {
			continue
		}
		parent, e := store.SafePath(s.Root, entry.Name())
		if e != nil {
			return nil, e
		}
		versions, e := os.ReadDir(parent)
		if e != nil {
			return nil, e
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i].Name() > versions[j].Name() })
		for _, ver := range versions {
			if !ver.IsDir() {
				continue
			}
			dir, e := store.SafePath(parent, ver.Name())
			if e != nil {
				return nil, e
			}
			b, e := os.ReadFile(filepath.Join(dir, "harness.yaml"))
			if e != nil {
				return nil, e
			}
			var meta struct {
				Command  string   `yaml:"command"`
				Args     []string `yaml:"args"`
				Disabled bool     `yaml:"disabled"`
				Custom   bool     `yaml:"custom"`
			}
			if e = yaml.Unmarshal(b, &meta); e != nil {
				return nil, e
			}
			la := localHarness{Name: entry.Name(), Path: meta.Command, Args: meta.Args, PackageDir: dir, Disabled: meta.Disabled}
			if !meta.Custom {
				sp, e := harness.LoadLocal(dir)
				if e != nil {
					return nil, e
				}
				if sp.Name != entry.Name() {
					return nil, store.ErrInvalid
				}
				la.Spec = &sp
			}
			la.Revision, e = harness.Revision(dir)
			if e != nil {
				return nil, e
			}
			out[la.Name] = la
			break
		}
	}
	return out, nil
}

// Save custom command settings as adapter files, never as endpoint fields.
func saveCommandAdapters(root string, commands map[string]HarnessConfig) error {
	for name, c := range commands {
		p, e := store.SafePath(root, name, "local", "harness.yaml")
		if e != nil {
			return e
		}
		doc := map[string]any{"name": name, "command": c.Command, "args": c.Args, "disabled": c.Disabled, "custom": true}
		if sp, ok := harness.Get(name); ok {
			if e = harness.CopyBuiltin(name, filepath.Dir(p)); e != nil {
				return e
			}
			b, _ := yaml.Marshal(sp)
			if e = yaml.Unmarshal(b, &doc); e != nil {
				return e
			}
			if c.Command != "" {
				doc["command"] = c.Command
			}
			doc["custom"] = false
			doc["args"] = c.Args
			doc["disabled"] = c.Disabled
		}
		if e = store.WriteYAML(p, doc); e != nil {
			return e
		}
	}
	return nil
}
