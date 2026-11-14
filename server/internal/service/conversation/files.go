// Conversation file operations apply business policy before calling the generic file storage service.
package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

const conversationFileNamespace = "conversations"

// OpenConversationFile applies conversation access rules before opening bytes.
// Storage backends only receive the namespace and relative storage key.
func (s *Service) OpenConversationFile(ctx context.Context, access core.AttachmentAccess, conversation, path string) (io.ReadCloser, int64, error) {
	if _, err := attachment.RelativePath(path); err != nil {
		return nil, 0, err
	}
	s.conversationFilesMu.RLock()
	defer s.conversationFilesMu.RUnlock()
	if _, err := s.attachmentConversation(ctx, s.DB, access, conversation, false); err != nil {
		return nil, 0, err
	}
	if s.Files == nil {
		return nil, 0, fmt.Errorf("file storage unavailable")
	}
	return s.Files.Get(ctx, conversationFileNamespace, conversation+"/"+path)
}

// Raw conversation files share the same backend and serialization as attachments.
// Referenced attachments remain immutable; ordinary unreferenced raw files may
// be overwritten according to the namespace's business policy.
func (s *Service) PutConversationFile(ctx context.Context, access core.AttachmentAccess, conversation, path string, source io.Reader) error {
	if _, err := attachment.RelativePath(path); err != nil {
		return err
	}
	if (access.DeviceID != "" || access.WorkID != "") && (path == "user_input" || strings.HasPrefix(path, "user_input/")) {
		return fmt.Errorf("user_input/ is reserved for user uploads")
	}
	if s.Files == nil {
		return fmt.Errorf("file storage unavailable")
	}
	s.conversationFilesMu.Lock()
	defer s.conversationFilesMu.Unlock()
	return s.Repository.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.attachmentConversation(ctx, tx, access, conversation, true); err != nil {
			return err
		}
		var registered int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attachments WHERE conversation_id=? AND relative_path=?`, conversation, path).Scan(&registered); err != nil {
			return err
		}
		if registered != 0 {
			return core.ErrAttachmentReferenced
		}
		return s.Files.Put(ctx, conversationFileNamespace, conversation+"/"+path, source, false)
	})
}

// Database deletion commits first. A backend failure is logged for operational retry;
// it must never roll back a completed database transaction or touch skill packages.
func (s *Service) CleanupFiles(ctx context.Context, id string) {
	s.conversationFilesMu.Lock()
	defer s.conversationFilesMu.Unlock()
	if s.Files != nil {
		if err := s.Files.DeletePrefix(ctx, conversationFileNamespace, id); err != nil {
			s.Log.Printf("conversation %s file cleanup: %v", id, err)
		}
	}
}
