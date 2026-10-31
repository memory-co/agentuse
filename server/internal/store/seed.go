package store

import (
	"context"
	"fmt"
	"github.com/memory-co/agentuse/internal/harness"
)

// seedBuiltinHarnesses 把 harness/*/harness.yaml 写进 harnesses 表。目录删了的不删行，免得 agent、会话的引用断掉。
func (s *Store) seedBuiltinHarnesses(ctx context.Context) error {
	builtins, err := harness.All()
	if err != nil {
		return fmt.Errorf("store: load harnesses: %w", err)
	}
	for _, sp := range builtins {
		a := Harness{Name: sp.Name, Kind: HarnessBuiltin, DisplayName: sp.DisplayName, Description: sp.Description}
		if a.DisplayName == "" {
			a.DisplayName = a.Name
		}
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO harnesses (name, kind, display_name, description, created_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, display_name = excluded.display_name, description = excluded.description`,
			a.Name, a.Kind, a.DisplayName, a.Description, s.unix())
		if err != nil {
			return fmt.Errorf("store: seed harnesses: %w", err)
		}
	}
	return nil
}
