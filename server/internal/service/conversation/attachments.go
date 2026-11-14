// Conversation attachment metadata, authorization and quotas belong to the conversation business layer.
package conversation

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) attachmentConversation(ctx context.Context, q core.Querier, a core.AttachmentAccess, id string, upload bool) (core.Conversation, error) {
	u := a.User
	if a.DeviceID != "" {
		var owner string
		if err := q.QueryRowContext(ctx, store.AttachmentConversationSelect, a.DeviceID, core.HashSecret(a.Token)).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return core.Conversation{}, core.ErrAttachmentUnauthorized
			}
			return core.Conversation{}, err
		}
		u = core.User{Role: core.RoleAdmin}
	}
	if a.DeviceID == "" {
		var authID, role string
		if err := q.QueryRowContext(ctx, store.AttachmentConversationSelect3, core.HashSecret(a.Token)).Scan(&authID, &role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return core.Conversation{}, core.ErrAttachmentUnauthorized
			}
			return core.Conversation{}, err
		}
		if authID != a.User.ID && role != core.RoleAdmin {
			return core.Conversation{}, core.ErrAttachmentUnauthorized
		}
		if authID == u.ID {
			u.Role = role
		}
	}
	ss, err := s.LookupConversation(ctx, q, u, id)
	if err != nil {
		return ss, err
	}
	if a.DeviceID != "" {
		var allowed int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM message_agents WHERE device_id=? AND conversation_id=? AND status IN ('sent','unknown','accepted','completed','failed','cancelled')`, a.DeviceID, ss.ID).Scan(&allowed); err != nil {
			return ss, err
		}
		// An explicit execution source narrows even a device that also executes
		// other branches. Device grants alone must not bypass the relation check.
		if a.SourceConversationID != "" && a.SourceConversationID != ss.ID {
			allowed = 0
		}
		if allowed == 0 {
			if upload || a.SourceConversationID == "" {
				return ss, core.ErrNotFound
			}
			var valid int
			err := q.QueryRowContext(ctx, `SELECT count(*) FROM conversations c JOIN message_agents p ON p.conversation_id=c.id WHERE c.id=? AND c.user_id=? AND p.device_id=? AND p.agent_id=? AND p.status IN ('sent','unknown','accepted','completed','failed','cancelled') AND ((c.mode='collaborative' AND c.parent_conversation_id IS NULL AND c.id=?) OR c.parent_conversation_id=?)`, a.SourceConversationID, ss.UserID, a.DeviceID, a.SourceAgentID, ss.ParentConversationID, ss.ID).Scan(&valid)
			if err != nil {
				return ss, err
			}
			if valid == 0 {
				return ss, core.ErrNotFound
			}
		}
		if upload {
			if err := s.attachmentRun(ctx, q, a, ss.ID, a.RunID, true); err != nil {
				if errors.Is(err, core.ErrNotFound) {
					return ss, core.ErrAttachmentRun
				}
				return ss, err
			}
		}
	}

	return ss, nil
}

func (s *Service) AttachmentConversation(ctx context.Context, a core.AttachmentAccess, id string, upload bool) (core.Conversation, error) {
	return s.attachmentConversation(ctx, s.DB, a, id, upload)
}

// CommitAttachment serializes authorization, quota and metadata with publishing the file.
func (s *Service) CommitAttachment(ctx context.Context, a core.AttachmentAccess, v *attachment.Attachment, max int64, publish func() error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentConversation(ctx, tx, a, v.ConversationID, true); err != nil {
		return err
	}
	if err = s.assignAttachmentPath(ctx, tx, v); err != nil {
		return err
	}
	var used int64
	if err = tx.QueryRowContext(ctx, store.CommitAttachmentSelect, v.ConversationID).Scan(&used); err != nil {
		return err
	}
	if v.SizeBytes > max || used > max-v.SizeBytes {
		return core.ErrAttachmentQuota
	}
	if err = publish(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, store.CommitAttachmentInsert, v.ID, v.ConversationID, v.Filename, v.ContentType, v.SizeBytes, v.SHA256, v.CreatedBy, v.Source, core.NullIfEmpty(v.DeviceID), v.CreatedAt, core.NullIfEmpty(v.CreatedByRunID), v.RelativePath)
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err == nil {
		if ss, e := s.GetConversation(ctx, core.User{Role: core.RoleAdmin}, v.ConversationID); e == nil {
			s.PublishConversationChange(ss, "attachments")
		}
	}
	return err
}

func (s *Service) NewAttachmentID() string { return core.RandomID("at_") }

func (s *Service) GetAttachment(ctx context.Context, conversation, id string) (attachment.Attachment, error) {
	return core.ScanAttachment(s.DB.QueryRowContext(ctx, core.AttachmentCols+store.GetAttachmentClause, conversation, id))
}

func (s *Service) ListAttachments(ctx context.Context, conversation, cursor string, limit int, run string) (attachment.Page, error) {
	out := attachment.Page{Attachments: []attachment.Attachment{}}
	var after struct {
		Conversation string
		Run          string
		Time         int64
		ID           string
	}
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(b, &after) != nil || after.Conversation != conversation || after.Run != run || !attachment.ValidID(after.ID, "at_") || after.Time < 0 {
			return out, core.ErrAttachmentCursor
		}
	}
	rows, err := s.DB.QueryContext(ctx, core.AttachmentCols+store.ListAttachmentsClause, conversation, run, run, after.Time, after.Time, after.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		a, e := core.ScanAttachment(rows)
		if e != nil {
			return out, e
		}
		out.Attachments = append(out.Attachments, a)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Attachments) > limit {
		out.Attachments = out.Attachments[:limit]
		last := out.Attachments[limit-1]
		b, _ := json.Marshal(struct {
			Conversation string
			Run          string
			Time         int64
			ID           string
		}{conversation, run, last.CreatedAt, last.ID})
		c := base64.RawURLEncoding.EncodeToString(b)
		out.NextCursor = &c
	}
	return out, nil
}

func (s *Service) deleteAttachment(ctx context.Context, a core.AttachmentAccess, conversation, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentConversation(ctx, tx, a, conversation, false); err != nil {
		return err
	}
	var referenced int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM messages m,json_each(m.attachments) f WHERE m.conversation_id=? AND json_extract(f.value,'$.id')=?`, conversation, id).Scan(&referenced); err != nil {
		return err
	}
	if referenced > 0 {
		return core.ErrAttachmentReferenced
	}

	r, err := tx.ExecContext(ctx, store.DeleteAttachmentDelete, conversation, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return core.ErrNotFound
	}
	err = tx.Commit()
	if err == nil {
		if ss, e := s.GetConversation(ctx, core.User{Role: core.RoleAdmin}, conversation); e == nil {
			s.PublishConversationChange(ss, "attachments")
		}
	}
	return err
}

func (s *Service) AttachmentExists(ctx context.Context, conversation, id string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, store.AttachmentExistsSelect, conversation, id).Scan(&n)
	return n > 0, err
}

// AttachmentRun checks attribution without changing the general conversation read scope.
func (s *Service) AttachmentRun(ctx context.Context, a core.AttachmentAccess, conversation, run string) error {
	return s.attachmentRun(ctx, s.DB, a, conversation, run, false)
}
func (s *Service) attachmentRun(ctx context.Context, q core.Querier, a core.AttachmentAccess, conversation, run string, active bool) error {
	var count int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE id=? AND conversation_id=? AND (?='' OR device_id=?) AND (?=0 OR status IN ('running','waiting'))`, run, conversation, a.DeviceID, a.DeviceID, active).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return core.ErrNotFound
	}
	return nil
}
