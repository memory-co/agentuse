// Device capabilities are snapshots of the connected Client and its harnesses.
package device

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) deviceCapabilities(ctx context.Context, id string) ([]string, error) {
	rows, err := store.Rows(ctx, s.DB, store.DeviceCapabilitiesSelect, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
