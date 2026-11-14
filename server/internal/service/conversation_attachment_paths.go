// Conversation attachment naming preserves message references across uploads and raw file writes.
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/memory-co/agentuse/internal/attachment"
)

var ErrAttachmentPathConflict = errors.New("文件路径与已有文件冲突")
var ErrAttachmentReferenced = errors.New("附件已被消息引用，请保留到对话删除时一起清理")

// Assign a unique path without replacing bytes already referenced by a message.
func (s *ConversationService) assignAttachmentPath(ctx context.Context, tx *sql.Tx, file *attachment.Attachment) error {
	requested, err := attachment.RelativePath(file.RelativePath)
	if err != nil {
		return err
	}
	candidate := requested
	extension := path.Ext(requested)
	stem := strings.TrimSuffix(path.Base(requested), extension)
	stored, err := s.files.List(ctx, conversationFileNamespace, file.ConversationID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for version := 2; version < 10000; version++ {
		if _, err = attachment.RelativePath(candidate); err != nil {
			return ErrAttachmentPathConflict
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT relative_path FROM attachments WHERE conversation_id=? AND (relative_path=? OR substr(relative_path,1,length(?)+1)=?||'/' OR substr(?,1,length(relative_path)+1)=relative_path||'/') LIMIT 1`, file.ConversationID, candidate, candidate, candidate, candidate).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			for _, entry := range stored {
				occupied := strings.TrimPrefix(entry.Path, file.ConversationID+"/")
				if candidate == occupied || strings.HasPrefix(occupied, candidate+"/") || strings.HasPrefix(candidate, occupied+"/") {
					existing = occupied
					err = nil
					break
				}
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			file.RelativePath = candidate
			return nil
		}
		if err != nil {
			return err
		}
		if strings.HasPrefix(candidate, existing+"/") {
			return ErrAttachmentPathConflict
		}
		candidate = path.Join(path.Dir(requested), fmt.Sprintf("%s (%d)%s", stem, version, extension))
	}
	return ErrAttachmentPathConflict
}
