package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/memory-co/agentuse/internal/attachment"
)

const attachmentSchema = `
CREATE TABLE IF NOT EXISTS attachments (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 filename TEXT NOT NULL, content_type TEXT NOT NULL, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),
 sha256 TEXT NOT NULL CHECK(length(sha256)=64), created_by TEXT NOT NULL REFERENCES users(id),
 source TEXT NOT NULL CHECK(source IN ('user','daemon','work')),
 daemon_id TEXT REFERENCES daemons(id) ON DELETE SET NULL, created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS attachments_session ON attachments(session_id,created_at,id);
CREATE TABLE IF NOT EXISTS daemon_capabilities (
 daemon_id TEXT NOT NULL REFERENCES daemons(id) ON DELETE CASCADE,
 capability TEXT NOT NULL, PRIMARY KEY(daemon_id,capability)
);`

var ErrAttachmentCursor = errors.New("游标不合法")

var ErrAttachmentUnauthorized = errors.New("附件凭据无效或已过期")

var ErrAttachmentQuota = errors.New("附件超过会话容量限制")
var ErrAttachmentCapability = errors.New("daemon 尚不支持附件，请先升级并连接一次")

// AttachmentAccess is revalidated inside the upload transaction, including credentials.
type AttachmentAccess struct {
	User                    User
	DaemonID, WorkID, Token string
	MessageID               int64
}

func (s *Store) attachmentSession(ctx context.Context, q querier, a AttachmentAccess, id string, upload bool) (Session, error) {
	u := a.User
	if a.DaemonID != "" {
		var owner string
		if err := q.QueryRowContext(ctx, `SELECT user_id FROM daemons WHERE id=? AND key_hash=?`, a.DaemonID, HashSecret(a.Token)).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Session{}, ErrAttachmentUnauthorized
			}
			return Session{}, err
		}
		u = User{Role: RoleAdmin}
	}
	if a.WorkID != "" {
		var n int
		err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_tokens t JOIN works w ON w.id=t.work_id JOIN sessions s ON s.id=w.coordinator_id WHERE t.work_id=? AND t.token_hash=? AND s.status IN ('running','waiting') AND s.lease_until>? AND t.message_id=(SELECT MAX(id) FROM messages WHERE session_id=s.id AND role='user')`, a.WorkID, HashSecret(a.Token), s.unix()).Scan(&n)
		if err != nil {
			return Session{}, err
		}
		if n == 0 {
			return Session{}, ErrAttachmentUnauthorized
		}
	}
	if a.DaemonID == "" && a.WorkID == "" {
		var authID, role string
		if err := q.QueryRowContext(ctx, `SELECT id,role FROM users WHERE token_hash=?`, HashSecret(a.Token)).Scan(&authID, &role); err != nil {
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
	if a.DaemonID != "" && ss.DaemonID != a.DaemonID {
		return Session{}, ErrNotFound
	}
	if a.WorkID != "" && ss.WorkID != a.WorkID {
		return Session{}, ErrNotFound
	}
	if upload {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM daemon_capabilities WHERE daemon_id=? AND capability=?`, ss.DaemonID, attachment.Capability).Scan(&n); err != nil {
			return ss, err
		}
		if n == 0 {
			return ss, ErrAttachmentCapability
		}
		if a.DaemonID != "" {
			err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=? AND lease_until>? AND `+runningWhere, ss.ID, s.unix(), a.DaemonID, a.MessageID, a.MessageID).Scan(&n)
			if err != nil {
				return ss, err
			}
			if n == 0 {
				return ss, ErrStaleMessage
			}
		}
	}
	return ss, nil
}
func (s *Store) AttachmentSession(ctx context.Context, a AttachmentAccess, id string, upload bool) (Session, error) {
	return s.attachmentSession(ctx, s.db, a, id, upload)
}

// CommitAttachment serializes authorization, quota and metadata with publishing the file.
func (s *Store) CommitAttachment(ctx context.Context, a AttachmentAccess, v attachment.Attachment, max int64, publish func() error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentSession(ctx, tx, a, v.SessionID, true); err != nil {
		return err
	}
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes),0) FROM attachments WHERE session_id=?`, v.SessionID).Scan(&used); err != nil {
		return err
	}
	if v.SizeBytes > max || used > max-v.SizeBytes {
		return ErrAttachmentQuota
	}
	if err = publish(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO attachments(id,session_id,filename,content_type,size_bytes,sha256,created_by,source,daemon_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, v.ID, v.SessionID, v.Filename, v.ContentType, v.SizeBytes, v.SHA256, v.CreatedBy, v.Source, nullIfEmpty(v.DaemonID), v.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) NewAttachmentID() string { return randomID("at_") }

const attachmentCols = `SELECT id,session_id,filename,content_type,size_bytes,sha256,created_by,source,COALESCE(daemon_id,''),created_at FROM attachments`

func scanAttachment(row scanner) (attachment.Attachment, error) {
	var a attachment.Attachment
	err := row.Scan(&a.ID, &a.SessionID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.CreatedBy, &a.Source, &a.DaemonID, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	a.DownloadPath = "/api/sessions/" + a.SessionID + "/attachments/" + a.ID + "/content"
	return a, err
}
func (s *Store) GetAttachment(ctx context.Context, session, id string) (attachment.Attachment, error) {
	return scanAttachment(s.db.QueryRowContext(ctx, attachmentCols+` WHERE session_id=? AND id=?`, session, id))
}
func (s *Store) ListAttachments(ctx context.Context, session, cursor string, limit int) (attachment.Page, error) {
	out := attachment.Page{Attachments: []attachment.Attachment{}}
	var after struct {
		Session string
		Time    int64
		ID      string
	}
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(b, &after) != nil || after.Session != session || !attachment.ValidID(after.ID, "at_") || after.Time < 0 {
			return out, ErrAttachmentCursor
		}
	}
	rows, err := s.db.QueryContext(ctx, attachmentCols+` WHERE session_id=? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`, session, after.Time, after.Time, after.ID, limit+1)
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
			Time    int64
			ID      string
		}{session, last.CreatedAt, last.ID})
		c := base64.RawURLEncoding.EncodeToString(b)
		out.NextCursor = &c
	}
	return out, nil
}
func (s *Store) DeleteAttachment(ctx context.Context, a AttachmentAccess, session, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.attachmentSession(ctx, tx, a, session, false); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE session_id=? AND id=?`, session, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
func (s *Store) AttachmentExists(ctx context.Context, session, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE session_id=? AND id=?`, session, id).Scan(&n)
	return n > 0, err
}
func (s *Store) daemonCapabilities(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT capability FROM daemon_capabilities WHERE daemon_id=? ORDER BY capability`, id)
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
