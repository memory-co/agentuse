// Conversation attachment naming preserves message references across uploads and raw file writes.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/memory-co/agentuse/server/internal/store"
	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/attachment"
)

// Assign a unique path without replacing bytes already referenced by a message.
func (s *Service) assignAttachmentPath(ctx context.Context, tx *gorm.DB, file *attachment.Attachment) error {
	requested, err := attachment.RelativePath(file.RelativePath)
	if err != nil {
		return err
	}
	candidate := requested
	extension := path.Ext(requested)
	stem := strings.TrimSuffix(path.Base(requested), extension)
	stored, err := s.Files.List(ctx, conversationFileNamespace, file.ConversationID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var occupiedPaths []string
	if err = tx.WithContext(ctx).Model(&store.Attachment{}).Where("conversation_id=?", file.ConversationID).Pluck("relative_path", &occupiedPaths).Error; err != nil {
		return err
	}
	for _, entry := range stored {
		occupiedPaths = append(occupiedPaths, strings.TrimPrefix(entry.Path, file.ConversationID+"/"))
	}
	for version := 2; version < 10000; version++ {
		if _, err = attachment.RelativePath(candidate); err != nil {
			return ErrAttachmentPathConflict
		}
		existing := ""
		for _, occupied := range occupiedPaths {
			if candidate == occupied || strings.HasPrefix(occupied, candidate+"/") || strings.HasPrefix(candidate, occupied+"/") {
				existing = occupied
				break
			}
		}
		if existing == "" {
			file.RelativePath = candidate
			return nil
		}
		if strings.HasPrefix(candidate, existing+"/") {
			return ErrAttachmentPathConflict
		}
		candidate = path.Join(path.Dir(requested), fmt.Sprintf("%s (%d)%s", stem, version, extension))
	}
	return ErrAttachmentPathConflict
}
