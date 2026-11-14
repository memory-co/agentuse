// Message attachment snapshots and output attribution belong to conversation orchestration.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/memory-co/agentuse/internal/attachment"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func (s *Service) messageAttachments(ctx context.Context, q querier, conversation string, ids []string) ([]sdk.Attachment, error) {
	if len(ids) > 64 {
		return nil, fmt.Errorf("一条消息最多包含 64 个附件")
	}
	files := []sdk.Attachment{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !attachment.ValidID(id, "at_") {
			return nil, fmt.Errorf("附件 ID 不合法")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		file, err := scanAttachment(q.QueryRowContext(ctx, attachmentCols+` WHERE conversation_id=? AND id=?`, conversation, id))
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}
func mustAttachmentJSON(files []sdk.Attachment) []byte { data, _ := json.Marshal(files); return data }
func (s *Service) attachRunOutputs(ctx context.Context, tx *sql.Tx, run Run, message string) error {
	files := []sdk.Attachment{}
	rows, err := tx.QueryContext(ctx, attachmentCols+` WHERE conversation_id=? AND created_by_run_id=? ORDER BY created_at,id`, run.ConversationID, run.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		file, e := scanAttachment(rows)
		if e != nil {
			rows.Close()
			return e
		}
		files = append(files, file)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE messages SET attachments=? WHERE id=?`, string(mustAttachmentJSON(files)), message)
	return err
}
