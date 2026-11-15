package device

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/store"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// ListHarnesses exposes reported identities only, never adapter configuration.
func (s *Service) ListHarnesses(ctx context.Context) ([]agentuse.Harness, error) {
	rows, err := store.Rows(ctx, s.DB, `SELECT DISTINCT harness FROM device_harnesses WHERE available=1 ORDER BY harness`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentuse.Harness{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, agentuse.Harness{Name: name, Kind: "reported", DisplayName: name})
	}
	return out, rows.Err()
}

// ReportedHarness is the public identity snapshot reported by a connected device.
type ReportedHarness struct {
	Capabilities []string `json:"capabilities,omitempty"`
	Revision     string   `json:"revision,omitempty"`
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
}
