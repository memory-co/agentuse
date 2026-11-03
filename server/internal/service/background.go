package service

import (
	"context"
	"time"
)

func (s *Service) Run(ctx context.Context) {
	if err := s.CleanupAttachments(ctx); err != nil {
		s.log.Printf("file cleanup: %v", err)
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
