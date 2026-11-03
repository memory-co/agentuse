package service

import "context"

// ListHarnesses exposes reported identities only, never adapter configuration.
func (s *Service) ListHarnesses(ctx context.Context) ([]Harness, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT harness FROM daemon_harnesses WHERE available=1 ORDER BY harness`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Harness{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, Harness{Name: name, Kind: "reported", DisplayName: name})
	}
	return out, rows.Err()
}
