package service

import (
	"context"
	"time"
)

func (s *Service) Run(ctx context.Context) {
	if err := s.ReconcileWorkNotifications(ctx); err != nil {
		s.log.Printf("work reconciliation: %v", err)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.Notifier.KickAll()
		}
	}
}
