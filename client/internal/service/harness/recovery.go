package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/memory-co/agentuse/client/internal/fileutil"
)

type RecoverySession struct{ Harness, Revision, EventsPath string }

// Recover only handles adapter cancellation at supplied event addresses.
// Session discovery and metadata recovery belong to run/session services.
func (s Service) Recover(ctx context.Context, sessions []RecoverySession) error {
	for _, metadata := range sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(metadata.EventsPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		adapter, err := s.resolveRevision(metadata.Harness, metadata.Revision)
		if err != nil {
			return err
		}
		if err = Cancel(adapter, metadata.EventsPath); err != nil {
			return err
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
	parent, err := fileutil.SafePath(s.Root, name)
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
		dir, err := fileutil.SafePath(parent, d.Name())
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
