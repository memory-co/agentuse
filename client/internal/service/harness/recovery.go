package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/memory-co/agentuse/client/internal/store"
	"os"
	"path/filepath"
)

// Recover cancels managed foreground work before old session metadata is released
// or quota reclamation can remove its workspace. It never reads native state/PIDs.
func (s Service) Recover(ctx context.Context, files *store.Ledger) error {
	endpoints, e := os.ReadDir(files.Root)
	if e != nil {
		return e
	}
	for _, ep := range endpoints {
		if !ep.IsDir() {
			continue
		}
		conversations, e := files.Reader().Conversations(ctx, ep.Name())
		if e != nil {
			return e
		}
		for _, c := range conversations {
			sessions, e := files.Reader().Sessions(ctx, ep.Name(), c.ID)
			if e != nil {
				return e
			}
			for _, session := range sessions {
				if session.Status != "running" {
					continue
				}
				events := filepath.Join(files.Root, ep.Name(), c.ID, "sessions", session.ID, "events.jsonl")
				if _, e = os.Stat(events); os.IsNotExist(e) {
					continue
				}
				if e != nil {
					return e
				}
				adapter, e := s.resolveRevision(session.Harness, session.HarnessRevision)
				if e != nil {
					return fmt.Errorf("recover session %s: %w", session.ID, e)
				}
				if e = Cancel(adapter, events); e != nil {
					return fmt.Errorf("recover session %s: %w", session.ID, e)
				}
			}
		}
	}
	return nil
}

// resolveRevision also finds a previously installed package after an upgrade.
// Recovery must not use the newly selected adapter for an older session.
func (s Service) resolveRevision(name, want string) (Adapter, error) {
	packages, err := s.Packages()
	if err != nil {
		return Adapter{}, err
	}
	if a, ok := packages[name]; ok && a.Revision == want {
		return a, nil
	}
	parent, err := store.SafePath(s.Root, name)
	if err != nil {
		return Adapter{}, err
	}
	dirs, err := os.ReadDir(parent)
	if err != nil {
		return Adapter{}, err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir, err := store.SafePath(parent, d.Name())
		if err != nil {
			return Adapter{}, err
		}
		rev, err := revision(dir)
		if err != nil {
			return Adapter{}, err
		}
		if rev != want {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			return Adapter{}, err
		}
		var m Manifest
		if err = json.Unmarshal(data, &m); err != nil {
			return Adapter{}, err
		}
		if m.Protocol != "harnessctl/2" || m.Entrypoint == "" || filepath.Base(m.Entrypoint) != m.Entrypoint {
			return Adapter{}, fmt.Errorf("invalid recovery adapter")
		}
		return Adapter{Name: name, Path: filepath.Join(dir, m.Entrypoint), PackageDir: dir, Revision: rev, Manifest: m}, nil
	}
	return Adapter{}, fmt.Errorf("adapter %s revision %s not installed", name, want)
}
