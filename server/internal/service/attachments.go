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

var ErrAttachmentCapability = errors.New("daemon 尚不支持附件，请先升级并连接一次")

func (s *Service) attachmentSession(ctx context.Context, q querier, a AttachmentAccess, id string, upload bool) (Session, error) {
	u := a.User
	if a.DaemonID != "" {
		var owner string
		if err := q.QueryRowContext(ctx, store.AttachmentSessionSelect, a.DaemonID, HashSecret(a.Token)).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Session{}, ErrAttachmentUnauthorized
			}
			return Session{}, err
		}
		u = User{Role: RoleAdmin}
	}
	if a.WorkID != "" {
		var n int
		err := q.QueryRowContext(ctx, store.AttachmentSessionSelect2, a.WorkID, HashSecret(a.Token), s.unix()).Scan(&n)
		if err != nil {
			return Session{}, err
		}
		if n == 0 {
			return Session{}, ErrAttachmentUnauthorized
		}

	}
	if a.DaemonID == "" && a.WorkID == "" {
		var authID, role string
		if err := q.QueryRowContext(ctx, store.AttachmentSessionSelect3, HashSecret(a.Token)).Scan(&authID, &role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Session{}, ErrAttachmentUnauthorized
			}
			return Session{}, err
		}
		if authID != a.User.ID && role != RoleAdmin {
			return Session{}, ErrAttachmentUnauthorized
		}
		if authID == u.ID {
			u.Role = role
		}
	}
	ss, err := s.getSession(ctx, q, u, id)
	if err != nil {
		return ss, err
	}
	if a.WorkID != "" && ss.WorkID != a.WorkID {
		return ss, ErrNotFound
	}
	if a.DaemonID != "" {
		var allowed int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM message_agents WHERE daemon_id=? AND session_id=? AND status IN ('sent','unknown','accepted','completed','failed','cancelled')`, a.DaemonID, ss.ID).Scan(&allowed); err != nil {
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

func (s *Service) AttachmentSession(ctx context.Context, a AttachmentAccess, id string, upload bool) (Session, error) {
	return s.attachmentSession(ctx, s.db, a, id, upload)
}

// CommitAttachment serializes authorization, quota and metadata with publishing the file.
func (s *Service) CommitAttachment(ctx context.Context, a AttachmentAccess, v attachment.Attachment, max int64, publish func() error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentSession(ctx, tx, a, v.SessionID, true); err != nil {
		return err
	}
	var used int64
	if err = tx.QueryRowContext(ctx, store.CommitAttachmentSelect, v.SessionID).Scan(&used); err != nil {
		return err
	}
	if v.SizeBytes > max || used > max-v.SizeBytes {
		return ErrAttachmentQuota
	}
	if err = publish(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, store.CommitAttachmentInsert, v.ID, v.SessionID, v.Filename, v.ContentType, v.SizeBytes, v.SHA256, v.CreatedBy, v.Source, nullIfEmpty(v.DaemonID), v.CreatedAt, nullIfEmpty(v.CreatedByRunID))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) NewAttachmentID() string { return randomID("at_") }

func (s *Service) GetAttachment(ctx context.Context, session, id string) (attachment.Attachment, error) {
	return scanAttachment(s.db.QueryRowContext(ctx, attachmentCols+store.GetAttachmentClause, session, id))
}

func (s *Service) ListAttachments(ctx context.Context, session, cursor string, limit int, run string) (attachment.Page, error) {
	out := attachment.Page{Attachments: []attachment.Attachment{}}
	var after struct {
		Session string
		Run     string
		Time    int64
		ID      string
	}
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(b, &after) != nil || after.Session != session || after.Run != run || !attachment.ValidID(after.ID, "at_") || after.Time < 0 {
			return out, ErrAttachmentCursor
		}
	}
	rows, err := s.db.QueryContext(ctx, attachmentCols+store.ListAttachmentsClause, session, run, run, after.Time, after.Time, after.ID, limit+1)
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
			Session string
			Run     string
			Time    int64
			ID      string
		}{session, run, last.CreatedAt, last.ID})
		c := base64.RawURLEncoding.EncodeToString(b)
		out.NextCursor = &c
	}
	return out, nil
}

func (s *Service) deleteAttachment(ctx context.Context, a AttachmentAccess, session, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentSession(ctx, tx, a, session, false); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, store.DeleteAttachmentDelete, session, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Service) AttachmentExists(ctx context.Context, session, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, store.AttachmentExistsSelect, session, id).Scan(&n)
	return n > 0, err
}

func (s *Service) daemonCapabilities(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, store.DaemonCapabilitiesSelect, id)
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
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE id=? AND session_id=? AND (?='' OR daemon_id=?) AND (?=0 OR status IN ('running','waiting'))`, run, conversation, a.DaemonID, a.DaemonID, active).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
