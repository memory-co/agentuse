// Conversation attachment metadata, authorization and quotas belong to the conversation business layer.
package service

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *DeviceService) deviceCapabilities(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, store.DeviceCapabilitiesSelect, id)
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
