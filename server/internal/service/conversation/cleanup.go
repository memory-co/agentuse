package conversation

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
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

// deleteWorkConversations participates in the Work owner's transaction. Each
// domain removes its rows before the parent work can be deleted.
func (s *Service) DeleteWorkConversations(ctx context.Context, tx *sql.Tx, work string) ([]string, error) {
	var busy int
	if err := tx.QueryRowContext(ctx, store.WorkConversationsBusySelect, work).Scan(&busy); err != nil {
		return nil, err
	}
	if busy > 0 {
		return nil, core.ErrConversationBusy
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM conversations WHERE work_id=?`, work)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err = s.deleteConversationContents(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, store.DeleteWorkConversationsDelete, work)
	return ids, err
}
