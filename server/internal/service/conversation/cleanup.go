package conversation

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/store"
)

// Device identity can be removed while published attachments remain available.
func (s *Service) DetachDeviceAttachments(ctx context.Context, tx *sql.Tx, device string) error {
	_, err := tx.ExecContext(ctx, `UPDATE attachments SET device_id=NULL WHERE device_id=?`, device)
	return err
}

func (s *Service) RenameConversation(ctx context.Context, tx *sql.Tx, id, title string) error {
	_, err := tx.ExecContext(ctx, store.RenameConversationUpdate, title, id)
	return err
}
