// Conversation attachment metadata, authorization and quotas belong to the conversation business layer.
package conversation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/memory-co/agentuse/internal/identity"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/attachment"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

func (s *Service) attachmentConversation(ctx context.Context, q *gorm.DB, a AttachmentAccess, id string, upload bool) (agentuse.Conversation, error) {
	tq := database.MessageTargets(s.DB, "m", "t")

	u := a.User
	if a.DeviceID != "" {
		p, err := s.RunService.AccessForRequestIn(ctx, q, a.Token)
		if err != nil || p.Preparing || p.DeviceID != a.DeviceID || p.UserID != a.User.ID || p.Claims.ConversationID != a.SourceConversationID || p.Claims.AgentID != a.SourceAgentID || (upload && p.RunID != a.RunID) {
			return agentuse.Conversation{}, ErrAttachmentUnauthorized
		}
		u = agentuse.User{ID: p.UserID, Role: agentuse.RoleUser}
	}

	if a.DeviceID == "" {
		var authID, role string
		if err := database.Row(ctx, q, database.AttachmentConversationSelect3, identity.HashSecret(a.Token)).Scan(&authID, &role); err != nil {
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
		if err := database.Row(ctx, q, `SELECT count(*) FROM `+tq.From+` WHERE `+tq.Device+`=? AND m.conversation_id=? AND `+tq.Dispatched, a.DeviceID, ss.ID).Scan(&allowed); err != nil {
			return ss, err
		}
		// An explicit execution source narrows even a device that also executes
		// other branches. Device grants alone must not bypass the relation check.
		if a.SourceConversationID != "" && a.SourceConversationID != ss.ID {
			allowed = 0
		}
		if allowed == 0 {
			if upload || a.SourceConversationID == "" {
				return ss, database.ErrNotFound
			}
			var valid int
			err := database.Row(ctx, q, `SELECT count(*) FROM conversations c JOIN runs r ON r.conversation_id=c.id WHERE c.id=? AND c.user_id=? AND r.device_id=? AND r.agent_id=? AND ((c.mode='collaborative' AND c.parent_conversation_id IS NULL AND c.id=?) OR c.parent_conversation_id=?)`, a.SourceConversationID, ss.UserID, a.DeviceID, a.SourceAgentID, ss.ParentConversationID, ss.ID).Scan(&valid)
			if err != nil {
				return ss, err
			}
			if valid == 0 {
				return ss, database.ErrNotFound
			}
		}
		if upload {
			if err := s.attachmentRun(ctx, q, a, ss.ID, a.RunID, true); err != nil {
				if errors.Is(err, database.ErrNotFound) {
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
	tx, err := database.Begin(ctx, s.DB)
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
	if err = database.Row(ctx, tx, database.CommitAttachmentSelect, v.ConversationID).Scan(&used); err != nil {
		return err
	}
	if v.SizeBytes > max || used > max-v.SizeBytes {
		return ErrAttachmentQuota
	}
	if _, err = s.attachmentConversation(ctx, tx, a, v.ConversationID, true); err != nil {
		return err
	}
	if err = publish(); err != nil {
		return err
	}
	if _, err = s.attachmentConversation(ctx, tx, a, v.ConversationID, true); err != nil {
		return err
	}
	pathHash := sha256.Sum256([]byte(v.RelativePath))
	_, err = database.Exec(ctx, tx, database.CommitAttachmentInsert, v.ID, v.ConversationID, v.Filename, v.ContentType, v.SizeBytes, v.SHA256, v.CreatedBy, v.Source, database.NullIfEmpty(v.DeviceID), v.CreatedAt, database.NullIfEmpty(v.CreatedByRunID), v.RelativePath, hex.EncodeToString(pathHash[:]))
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

func (s *Service) NewAttachmentID() string { return identity.NewID("at_") }

func (s *Service) GetAttachment(ctx context.Context, conversation, id string) (attachment.Attachment, error) {
	return database.ScanAttachment(database.Row(ctx, s.DB, database.AttachmentCols+database.GetAttachmentClause, conversation, id))
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
	rows, err := database.Rows(ctx, s.DB, database.AttachmentCols+database.ListAttachmentsClause, conversation, run, run, after.Time, after.Time, after.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		a, e := database.ScanAttachment(rows)
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
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentConversation(ctx, tx, a, conversation, false); err != nil {
		return err
	}
	var referenced int
	if err = database.Row(ctx, tx, database.AttachmentReferences(s.DB), conversation, id).Scan(&referenced); err != nil {
		return err
	}
	if referenced > 0 {
		return ErrAttachmentReferenced
	}

	r, err := database.Exec(ctx, tx, database.DeleteAttachmentDelete, conversation, id)
	if err != nil {
		return err
	}
	if r == 0 {
		return database.ErrNotFound
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
	err := database.Row(ctx, s.DB, database.AttachmentExistsSelect, conversation, id).Scan(&n)
	return n > 0, err
}

// AttachmentRun checks attribution without changing the general conversation read scope.
func (s *Service) AttachmentRun(ctx context.Context, a AttachmentAccess, conversation, run string) error {
	return s.attachmentRun(ctx, s.DB, a, conversation, run, false)
}
func (s *Service) attachmentRun(ctx context.Context, q *gorm.DB, a AttachmentAccess, conversation, run string, active bool) error {
	var count int
	err := database.Row(ctx, q, `SELECT count(*) FROM runs WHERE id=? AND conversation_id=? AND (?='' OR device_id=?) AND (?=0 OR status IN ('running','waiting'))`, run, conversation, a.DeviceID, a.DeviceID, active).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return database.ErrNotFound
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
