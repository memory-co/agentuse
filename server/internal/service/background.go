package service

import (
	"context"
	"time"
)

// Run 跑后台任务(收回过期租约),直到 ctx 结束。
func (s *Service) Run(ctx context.Context) {
	if err := s.CleanupAttachments(ctx); err != nil {
		s.log.Printf("attachment cleanup: %v", err)
	}
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			if err := s.CleanupAttachments(ctx); err != nil {
				s.log.Printf("attachment cleanup: %v", err)
			}
		case <-t.C:
			if err := s.RequeueRuns(ctx); err != nil {
				s.log.Printf("runs: %v", err)
			}
			s.Notifier.KickAll()

		}
	}
}
