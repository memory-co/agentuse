package conversation

import (
	"context"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/server/internal/store"
)

// Device identity can be removed while published attachments remain available.
func (s *Service) DetachDeviceAttachments(ctx context.Context, tx *gorm.DB, device string) error {
	_, err := store.Exec(ctx, tx, `UPDATE attachments SET device_id=NULL WHERE device_id=?`, device)
	return err
}

func (s *Service) RenameConversation(ctx context.Context, tx *gorm.DB, id, title string) error {
	_, err := store.Exec(ctx, tx, store.RenameConversationUpdate, title, id)
	return err
}
