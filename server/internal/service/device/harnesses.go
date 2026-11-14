package device

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

// ListHarnesses exposes reported identities only, never adapter configuration.
func (s *Service) ListHarnesses(ctx context.Context) ([]core.Harness, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT harness FROM device_harnesses WHERE available=1 ORDER BY harness`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Harness{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, core.Harness{Name: name, Kind: "reported", DisplayName: name})
	}
	return out, rows.Err()
}
