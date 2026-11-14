// Conversation attachment metadata, authorization and quotas belong to the conversation business layer.
package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/memory-co/agentuse/server/internal/store"

	"github.com/memory-co/agentuse/internal/attachment"
)

var ErrAttachmentRun = errors.New("上传附件需要当前设备在本会话的活动 run")

var ErrAttachmentCursor = errors.New("游标不合法")

var ErrAttachmentUnauthorized = errors.New("附件凭据无效或已过期")

var ErrAttachmentQuota = errors.New("附件超过会话容量限制")

var ErrAttachmentCapability = errors.New("device 尚不支持附件，请先升级并连接一次")

func (s *Service) attachmentConversation(ctx context.Context, q querier, a AttachmentAccess, id string, upload bool) (Conversation, error) {
	u := a.User
	if a.DeviceID != "" {
		var owner string
		if err := q.QueryRowContext(ctx, store.AttachmentConversationSelect, a.DeviceID, HashSecret(a.Token)).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Conversation{}, ErrAttachmentUnauthorized
			}
			return Conversation{}, err
		}
		u = User{Role: RoleAdmin}
	}
	if a.WorkID != "" {
		var n int
		err := q.QueryRowContext(ctx, store.AttachmentConversationSelect2, a.WorkID, HashSecret(a.Token)).Scan(&n)
		if err != nil {
			return Conversation{}, err
		}
		if n == 0 {
			return Conversation{}, ErrAttachmentUnauthorized
		}

	}
	if a.DeviceID == "" && a.WorkID == "" {
		var authID, role string
		if err := q.QueryRowContext(ctx, store.AttachmentConversationSelect3, HashSecret(a.Token)).Scan(&authID, &role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Conversation{}, ErrAttachmentUnauthorized
			}
			return Conversation{}, err
		}
		if authID != a.User.ID && role != RoleAdmin {
			return Conversation{}, ErrAttachmentUnauthorized
		}
		if authID == u.ID {
			u.Role = role
		}
	}
	ss, err := s.getConversation(ctx, q, u, id)
	if err != nil {
		return ss, err
	}
	if a.WorkID != "" && ss.WorkID != a.WorkID {
		return ss, ErrNotFound
	}
	if a.DeviceID != "" {
		var allowed int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM message_agents WHERE device_id=? AND conversation_id=? AND status IN ('sent','unknown','accepted','completed','failed','cancelled')`, a.DeviceID, ss.ID).Scan(&allowed); err != nil {
			return ss, err
		}
		if allowed == 0 {
			return ss, ErrNotFound
		}
		if upload {
			if err := s.attachmentRun(ctx, q, a, ss.ID, a.RunID, true); err != nil {
				if errors.Is(err, ErrNotFound) {
					return ss, ErrAttachmentRun
				}
				return ss, err
			}
		}
	}

	return ss, nil
}

func (s *Service) AttachmentConversation(ctx context.Context, a AttachmentAccess, id string, upload bool) (Conversation, error) {
	return s.attachmentConversation(ctx, s.db, a, id, upload)
}

// CommitAttachment serializes authorization, quota and metadata with publishing the file.
func (s *Service) CommitAttachment(ctx context.Context, a AttachmentAccess, v *attachment.Attachment, max int64, publish func() error) error {
	tx, err := s.db.BeginTx(ctx, nil)
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
		return ErrAttachmentQuota
	}
	if err = publish(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, store.CommitAttachmentInsert, v.ID, v.ConversationID, v.Filename, v.ContentType, v.SizeBytes, v.SHA256, v.CreatedBy, v.Source, nullIfEmpty(v.DeviceID), v.CreatedAt, nullIfEmpty(v.CreatedByRunID), v.RelativePath)
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err == nil {
		if ss, e := s.GetConversation(ctx, User{Role: RoleAdmin}, v.ConversationID); e == nil {
			s.publishConversationChange(ss, "attachments")
		}
	}
	return err
}

func (s *Service) NewAttachmentID() string { return randomID("at_") }

func (s *Service) GetAttachment(ctx context.Context, conversation, id string) (attachment.Attachment, error) {
	return scanAttachment(s.db.QueryRowContext(ctx, attachmentCols+store.GetAttachmentClause, conversation, id))
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
	rows, err := s.db.QueryContext(ctx, attachmentCols+store.ListAttachmentsClause, conversation, run, run, after.Time, after.Time, after.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		a, e := scanAttachment(rows)
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
	tx, err := s.db.BeginTx(ctx, nil)
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
		return ErrAttachmentReferenced
	}

	r, err := tx.ExecContext(ctx, store.DeleteAttachmentDelete, conversation, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	err = tx.Commit()
	if err == nil {
		if ss, e := s.GetConversation(ctx, User{Role: RoleAdmin}, conversation); e == nil {
			s.publishConversationChange(ss, "attachments")
		}
	}
	return err
}

func (s *Service) AttachmentExists(ctx context.Context, conversation, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, store.AttachmentExistsSelect, conversation, id).Scan(&n)
	return n > 0, err
}

func (s *Service) deviceCapabilities(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, store.DeviceCapabilitiesSelect, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AttachmentRun checks attribution without changing the general conversation read scope.
func (s *Service) AttachmentRun(ctx context.Context, a AttachmentAccess, conversation, run string) error {
	return s.attachmentRun(ctx, s.db, a, conversation, run, false)
}
func (s *Service) attachmentRun(ctx context.Context, q querier, a AttachmentAccess, conversation, run string, active bool) error {
	var count int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE id=? AND conversation_id=? AND (?='' OR device_id=?) AND (?=0 OR status IN ('running','waiting'))`, run, conversation, a.DeviceID, a.DeviceID, active).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
