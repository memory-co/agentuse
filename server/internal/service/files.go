package service

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/memory-co/agentuse/internal/attachment"
)

// Raw conversation files share the same backend and serialization as attachments.
// Referenced attachments remain immutable; ordinary unreferenced raw files may
// be overwritten according to the namespace's business policy.
func (s *Service) PutConversationFile(ctx context.Context, access AttachmentAccess, conversation, path string, source io.Reader) error {
	if _, err := attachment.RelativePath(path); err != nil {
		return err
	}
	if (access.DeviceID != "" || access.WorkID != "") && (path == "user_input" || strings.HasPrefix(path, "user_input/")) {
		return fmt.Errorf("user_input/ is reserved for user uploads")
	}
	if s.Files == nil {
		return fmt.Errorf("file storage unavailable")
	}
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	return s.repository.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.attachmentConversation(ctx, tx, access, conversation, true); err != nil {
			return err
		}
		var registered int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attachments WHERE conversation_id=? AND relative_path=?`, conversation, path).Scan(&registered); err != nil {
			return err
		}
		if registered != 0 {
			return ErrAttachmentReferenced
		}
		return s.Files.Put(ctx, "conversations", conversation+"/"+path, source, false)
	})
}

// Database deletion commits first. A backend failure is logged for operational retry;
// it must never roll back a completed database transaction or touch skill packages.
func (s *Service) cleanupConversationFiles(ctx context.Context, id string) {
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	if s.Files != nil {
		if err := s.Files.DeletePrefix(ctx, "conversations", id); err != nil {
			s.log.Printf("conversation %s file cleanup: %v", id, err)
		}
	}
}
