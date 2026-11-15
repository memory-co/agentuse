// Device capabilities are snapshots of the connected Client and its harnesses.
package device

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/database"
)

func (s *Service) deviceCapabilities(ctx context.Context, id string) ([]string, error) {
	rows, err := database.Rows(ctx, s.DB, database.DeviceCapabilitiesSelect, id)
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
