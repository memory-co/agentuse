package service

import "context"

// Database deletion commits first. A backend failure is logged for operational retry;
// it must never roll back a completed database transaction or touch skill packages.
func (s *Service) cleanupConversationFiles(ctx context.Context, id string) {
	if s.Files != nil {
		if err := s.Files.DeletePrefix(ctx, "conversations", id); err != nil {
			s.log.Printf("conversation %s file cleanup: %v", id, err)
		}
	}
}
