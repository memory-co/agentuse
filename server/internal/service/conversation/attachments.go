// Conversation attachment metadata, authorization and quotas belong to the conversation business layer.
package conversation

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/attachment"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) attachmentConversation(ctx context.Context, q *gorm.DB, a AttachmentAccess, id string, upload bool) (agentuse.Conversation, error) {
	tq := store.MessageTargets(s.DB, "m", "t")

	u := a.User
	if a.DeviceID != "" {
		var owner string
		if err := store.Row(ctx, q, store.AttachmentConversationSelect, a.DeviceID, store.HashSecret(a.Token)).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return agentuse.Conversation{}, ErrAttachmentUnauthorized
			}
			return agentuse.Conversation{}, err
		}
		u = agentuse.User{Role: agentuse.RoleAdmin}
	}
	if a.DeviceID == "" {
		var authID, role string
		if err := store.Row(ctx, q, store.AttachmentConversationSelect3, store.HashSecret(a.Token)).Scan(&authID, &role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return agentuse.Conversation{}, ErrAttachmentUnauthorized
			}
			return agentuse.Conversation{}, err
		}
		if authID != a.User.ID && role != agentuse.RoleAdmin {
			return agentuse.Conversation{}, ErrAttachmentUnauthorized
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
		if err := store.Row(ctx, q, `SELECT count(*) FROM `+tq.From+` WHERE `+tq.Device+`=? AND m.conversation_id=? AND `+tq.Dispatched, a.DeviceID, ss.ID).Scan(&allowed); err != nil {
			return ss, err
		}
		// An explicit execution source narrows even a device that also executes
		// other branches. Device grants alone must not bypass the relation check.
		if a.SourceConversationID != "" && a.SourceConversationID != ss.ID {
			allowed = 0
		}
		if allowed == 0 {
			if upload || a.SourceConversationID == "" {
				return ss, store.ErrNotFound
			}
			var valid int
			err := store.Row(ctx, q, `SELECT count(*) FROM conversations c JOIN runs r ON r.conversation_id=c.id WHERE c.id=? AND c.user_id=? AND r.device_id=? AND r.agent_id=? AND ((c.mode='collaborative' AND c.parent_conversation_id IS NULL AND c.id=?) OR c.parent_conversation_id=?)`, a.SourceConversationID, ss.UserID, a.DeviceID, a.SourceAgentID, ss.ParentConversationID, ss.ID).Scan(&valid)
			if err != nil {
				return ss, err
			}
			if valid == 0 {
				return ss, store.ErrNotFound
			}
		}
		if upload {
			if err := s.attachmentRun(ctx, q, a, ss.ID, a.RunID, true); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return ss, ErrAttachmentRun
				}
				return ss, err
			}
		}
	}

	return ss, nil
}

func (s *Service) AttachmentConversation(ctx context.Context, a AttachmentAccess, id string, upload bool) (agentuse.Conversation, error) {
	return s.attachmentConversation(ctx, s.DB, a, id, upload)
}

// CommitAttachment serializes authorization, quota and metadata with publishing the file.
func (s *Service) CommitAttachment(ctx context.Context, a AttachmentAccess, v *attachment.Attachment, max int64, publish func() error) error {
	tx, err := store.Begin(ctx, s.DB)
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
	if err = store.Row(ctx, tx, store.CommitAttachmentSelect, v.ConversationID).Scan(&used); err != nil {
		return err
	}
	if v.SizeBytes > max || used > max-v.SizeBytes {
		return ErrAttachmentQuota
	}
	if err = publish(); err != nil {
		return err
	}
	_, err = store.Exec(ctx, tx, store.CommitAttachmentInsert, v.ID, v.ConversationID, v.Filename, v.ContentType, v.SizeBytes, v.SHA256, v.CreatedBy, v.Source, store.NullIfEmpty(v.DeviceID), v.CreatedAt, store.NullIfEmpty(v.CreatedByRunID), v.RelativePath, store.HashSecret(v.RelativePath))
	if err != nil {
		return err
	}
	err = tx.Commit().Error
	if err == nil {
		if ss, e := s.GetConversation(ctx, agentuse.User{Role: agentuse.RoleAdmin}, v.ConversationID); e == nil {
			s.PublishConversationChange(ss, "attachments")
		}
	}
	return err
}

func (s *Service) NewAttachmentID() string { return store.RandomID("at_") }

func (s *Service) GetAttachment(ctx context.Context, conversation, id string) (attachment.Attachment, error) {
	return store.ScanAttachment(store.Row(ctx, s.DB, store.AttachmentCols+store.GetAttachmentClause, conversation, id))
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
			return out, ErrAttachmentCursor
		}
	}
	rows, err := store.Rows(ctx, s.DB, store.AttachmentCols+store.ListAttachmentsClause, conversation, run, run, after.Time, after.Time, after.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		a, e := store.ScanAttachment(rows)
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

func (s *Service) deleteAttachment(ctx context.Context, a AttachmentAccess, conversation, id string) error {
	tx, err := store.Begin(ctx, s.DB)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentConversation(ctx, tx, a, conversation, false); err != nil {
		return err
	}
	var referenced int
	if err = store.Row(ctx, tx, store.AttachmentReferences(s.DB), conversation, id).Scan(&referenced); err != nil {
		return err
	}
	if referenced > 0 {
		return ErrAttachmentReferenced
	}

	r, err := store.Exec(ctx, tx, store.DeleteAttachmentDelete, conversation, id)
	if err != nil {
		return err
	}
	if r == 0 {
		return store.ErrNotFound
	}
	err = tx.Commit().Error
	if err == nil {
		if ss, e := s.GetConversation(ctx, agentuse.User{Role: agentuse.RoleAdmin}, conversation); e == nil {
			s.PublishConversationChange(ss, "attachments")
		}
	}
	return err
}

func (s *Service) AttachmentExists(ctx context.Context, conversation, id string) (bool, error) {
	var n int
	err := store.Row(ctx, s.DB, store.AttachmentExistsSelect, conversation, id).Scan(&n)
	return n > 0, err
}

// AttachmentRun checks attribution without changing the general conversation read scope.
func (s *Service) AttachmentRun(ctx context.Context, a AttachmentAccess, conversation, run string) error {
	return s.attachmentRun(ctx, s.DB, a, conversation, run, false)
}
func (s *Service) attachmentRun(ctx context.Context, q *gorm.DB, a AttachmentAccess, conversation, run string, active bool) error {
	var count int
	err := store.Row(ctx, q, `SELECT count(*) FROM runs WHERE id=? AND conversation_id=? AND (?='' OR device_id=?) AND (?=0 OR status IN ('running','waiting'))`, run, conversation, a.DeviceID, a.DeviceID, active).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return store.ErrNotFound
	}
	return nil
}

// AttachmentAccess is revalidated inside the upload transaction.
type AttachmentAccess struct {
	User                                agentuse.User
	DeviceID, Token                     string
	SourceConversationID, SourceAgentID string
	RunID                               string
}
