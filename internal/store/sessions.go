package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	SessionIdle    = "idle"
	SessionPending = "pending"
	SessionRunning = "running"

	RoleUserMsg      = "user"
	RoleAssistantMsg = "assistant"

	EventProgress = "progress"
	EventDone     = "done"
	EventFailed   = "failed"
)

type Session struct {
	ID               string `json:"id"`
	UserID           string `json:"user_id"`
	Harness          string `json:"harness"`
	DaemonID         string `json:"daemon_id,omitempty"`
	Cwd              string `json:"cwd,omitempty"`
	HarnessSessionID string `json:"harness_session_id,omitempty"`
	Title            string `json:"title"`
	Status           string `json:"status"`
	LeaseUntil       *int64 `json:"lease_until,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

type Message struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CreatedAt int64           `json:"created_at"`
}

type SessionEvent struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	MessageID int64           `json:"message_id"`
	DaemonID  string          `json:"daemon_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

// Claimed 是 daemon 领到的活：会话和它最后一条用户消息。
type Claimed struct {
	Session Session
	Message Message
}

var (
	// ErrStaleMessage 表示 daemon 回报的消息已经不归它了(租约过期被收回、或已经回过)。
	ErrStaleMessage = errors.New("store: message is not running on this daemon")
	// ErrSessionBusy 表示会话还在等 harness 回复，不能再发消息。
	ErrSessionBusy = errors.New("会话还在等 harness 回复")
	// ErrSessionOrphaned 表示会话的 daemon 已删除，续不上了。
	ErrSessionOrphaned = errors.New("会话原来的 daemon 已删除，不能继续")
)

// CreateSession 建会话并写入第一条用户消息，会话为 pending。harness 必须登记过。
func (s *Store) CreateSession(ctx context.Context, userID, harness, cwd, title string, content json.RawMessage) (Session, Message, error) {
	if !json.Valid(content) {
		return Session{}, Message{}, errors.New("input 必须是 JSON")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, Message{}, err
	}
	defer tx.Rollback()
	now := s.unix()
	ss := Session{ID: randomID("ss_"), UserID: userID, Harness: harness, Cwd: cwd, Title: title, Status: SessionPending, CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, harness, cwd, title, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ss.ID, userID, harness, nullIfEmpty(cwd), title, ss.Status, now, now)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return Session{}, Message{}, fmt.Errorf("harness %q 没有登记", harness)
	}
	if err != nil {
		return Session{}, Message{}, err
	}
	m, err := insertMessage(ctx, tx, ss.ID, RoleUserMsg, content, now)
	if err != nil {
		return Session{}, Message{}, err
	}
	return ss, m, tx.Commit()
}

// AddMessage 在 idle 的会话里发下一条用户消息，会话回到 pending。
// TODO: 插话(harness 还在跑时追加消息)以后用 harness 的 hook 来做。
func (s *Store) AddMessage(ctx context.Context, u User, sessionID string, content json.RawMessage) (Session, Message, error) {
	if !json.Valid(content) {
		return Session{}, Message{}, errors.New("input 必须是 JSON")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, Message{}, err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, sessionID)
	if err != nil {
		return Session{}, Message{}, err
	}
	if ss.Status != SessionIdle {
		return Session{}, Message{}, ErrSessionBusy
	}
	// idle 的会话一定被 daemon 跑过;daemon_id 为空说明那台 daemon 被删了。
	if ss.DaemonID == "" {
		return Session{}, Message{}, ErrSessionOrphaned
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = 'pending', updated_at = ? WHERE id = ?`, now, ss.ID); err != nil {
		return Session{}, Message{}, err
	}
	m, err := insertMessage(ctx, tx, ss.ID, RoleUserMsg, content, now)
	if err != nil {
		return Session{}, Message{}, err
	}
	ss.Status, ss.UpdatedAt = SessionPending, now
	return ss, m, tx.Commit()
}

func insertMessage(ctx context.Context, tx *sql.Tx, sessionID, role string, content json.RawMessage, now int64) (Message, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO messages (session_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, role, string(content), now)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
	return Message{ID: id, SessionID: sessionID, Role: role, Content: content, CreatedAt: now}, nil
}

// ClaimSession 给 daemon 挑一个 pending 的会话：同一用户、harness 在这台 daemon 上可用且已开启，
// 会话没绑定 daemon 或绑定的就是这台;最早的优先。领走后会话绑定到这台 daemon。
// 没有可领的返回 ErrNotFound。
func (s *Store) ClaimSession(ctx context.Context, d Daemon, leaseSeconds int64) (Claimed, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Claimed{}, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `
		SELECT s.id FROM sessions s
		WHERE s.status = 'pending' AND s.user_id = ? AND (s.daemon_id IS NULL OR s.daemon_id = ?)
		  AND EXISTS (SELECT 1 FROM daemon_harnesses da
		              WHERE da.daemon_id = ? AND da.user_id = s.user_id AND da.harness = s.harness
		                AND da.available = 1 AND da.enabled = 1)
		ORDER BY s.updated_at, s.id LIMIT 1`, d.UserID, d.ID, d.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Claimed{}, ErrNotFound
	}
	if err != nil {
		return Claimed{}, err
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET status = 'running', daemon_id = ?, lease_until = ?, updated_at = ? WHERE id = ?`,
		d.ID, now+leaseSeconds, now, id); err != nil {
		return Claimed{}, err
	}
	ss, err := s.scanSession(tx.QueryRowContext(ctx, sessionCols+` WHERE id = ?`, id))
	if err != nil {
		return Claimed{}, err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, messageCols+` WHERE session_id = ? AND role = 'user' ORDER BY id DESC LIMIT 1`, id))
	if err != nil {
		return Claimed{}, err
	}
	return Claimed{Session: ss, Message: m}, tx.Commit()
}

// runningWhere 匹配「这台 daemon 正在回这条用户消息」的会话:running、归这台 daemon、消息是最后一条用户消息。
const runningWhere = `status = 'running' AND daemon_id = ?
	AND id = (SELECT session_id FROM messages WHERE id = ? AND role = 'user')
	AND ? = (SELECT MAX(m.id) FROM messages m WHERE m.session_id = sessions.id AND m.role = 'user')`

// RenewLease 给 daemon 正在回的消息所在的会话续租。
func (s *Store) RenewLease(ctx context.Context, daemonID string, messageID int64, leaseSeconds int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET lease_until = ? WHERE `+runningWhere,
		s.unix()+leaseSeconds, daemonID, messageID, messageID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleMessage
	}
	return nil
}

// AddEvent 记一条 daemon 回报，并续租;done / failed 时追加 assistant 消息、会话回到 idle,
// payload 里带了 session_id 就记成 harness 的会话 id。
func (s *Store) AddEvent(ctx context.Context, daemonID string, messageID int64, kind string, payload json.RawMessage, leaseSeconds int64) error {
	switch kind {
	case EventProgress, EventDone, EventFailed:
	default:
		return fmt.Errorf("未知回报类型 %q", kind)
	}
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	if !json.Valid(payload) {
		return errors.New("payload 必须是 JSON")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.unix()
	var sessionID string
	if kind == EventProgress {
		err = tx.QueryRowContext(ctx,
			`UPDATE sessions SET lease_until = ? WHERE `+runningWhere+` RETURNING id`,
			now+leaseSeconds, daemonID, messageID, messageID).Scan(&sessionID)
	} else {
		var r struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(payload, &r)
		err = tx.QueryRowContext(ctx,
			`UPDATE sessions SET status = 'idle', lease_until = NULL, updated_at = ?,
			   harness_session_id = COALESCE(NULLIF(?, ''), harness_session_id)
			 WHERE `+runningWhere+` RETURNING id`,
			now, r.SessionID, daemonID, messageID, messageID).Scan(&sessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStaleMessage
	}
	if err != nil {
		return err
	}
	if kind != EventProgress {
		if _, err := insertMessage(ctx, tx, sessionID, RoleAssistantMsg, payload, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO session_events (session_id, message_id, daemon_id, kind, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, messageID, daemonID, kind, string(payload), now); err != nil {
		return err
	}
	return tx.Commit()
}

// RequeueExpired 把租约过期的会话放回 pending(还是给同一台 daemon),返回受影响的用户 id(用来叫醒他们的 daemon)。
func (s *Store) RequeueExpired(ctx context.Context) ([]string, error) {
	return s.requeue(ctx, `status = 'running' AND lease_until < ?`, s.unix())
}

// RequeueDaemonSessions 把某台 daemon 名下 running、但最后一条用户消息不在 keep 里的会话放回 pending。
// daemon 重连时调用:它没在跑的就不该还是 running。
func (s *Store) RequeueDaemonSessions(ctx context.Context, daemonID string, keep []int64) ([]string, error) {
	where := `status = 'running' AND daemon_id = ?`
	args := []any{daemonID}
	if len(keep) > 0 {
		where += ` AND (SELECT MAX(m.id) FROM messages m WHERE m.session_id = sessions.id AND m.role = 'user') NOT IN (?` +
			strings.Repeat(`, ?`, len(keep)-1) + `)`
		for _, k := range keep {
			args = append(args, k)
		}
	}
	return s.requeue(ctx, where, args...)
}

func (s *Store) requeue(ctx context.Context, where string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`UPDATE sessions SET status = 'pending', lease_until = NULL, updated_at = ?
		 WHERE `+where+` RETURNING user_id`, append([]any{s.unix()}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		if !seen[u] {
			seen[u] = true
			users = append(users, u)
		}
	}
	return users, rows.Err()
}

// failDaemonSessions 在删除 daemon 前调用：它名下还没回完的会话回到 idle,并追加一条失败的 assistant 消息。
func (s *Store) failDaemonSessions(ctx context.Context, tx *sql.Tx, daemonID string) error {
	now := s.unix()
	rows, err := tx.QueryContext(ctx,
		`UPDATE sessions SET status = 'idle', lease_until = NULL, updated_at = ?
		 WHERE daemon_id = ? AND status IN ('pending', 'running') RETURNING id`, now, daemonID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	content := json.RawMessage(`{"ok":false,"error":"daemon 已删除"}`)
	for _, id := range ids {
		if _, err := insertMessage(ctx, tx, id, RoleAssistantMsg, content, now); err != nil {
			return err
		}
	}
	return nil
}

const sessionCols = `SELECT id, user_id, harness, daemon_id, cwd, harness_session_id, title, status, lease_until, created_at, updated_at FROM sessions`

func (s *Store) scanSession(row scanner) (Session, error) {
	var ss Session
	var daemon, cwd, harness sql.NullString
	var lease sql.NullInt64
	err := row.Scan(&ss.ID, &ss.UserID, &ss.Harness, &daemon, &cwd, &harness, &ss.Title, &ss.Status, &lease, &ss.CreatedAt, &ss.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	ss.DaemonID, ss.Cwd, ss.HarnessSessionID = daemon.String, cwd.String, harness.String
	ss.LeaseUntil = nullInt(lease)
	return ss, nil
}

const messageCols = `SELECT id, session_id, role, content, created_at FROM messages`

func scanMessage(row scanner) (Message, error) {
	var m Message
	var content string
	err := row.Scan(&m.ID, &m.SessionID, &m.Role, &content, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	m.Content = json.RawMessage(content)
	return m, err
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) getSession(ctx context.Context, q querier, u User, id string) (Session, error) {
	query := sessionCols + ` WHERE id = ?`
	args := []any{id}
	if !u.IsAdmin() {
		query += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	return s.scanSession(q.QueryRowContext(ctx, query, args...))
}

func (s *Store) GetSession(ctx context.Context, u User, id string) (Session, error) {
	return s.getSession(ctx, s.db, u, id)
}

// ListSessions 列最近的会话;admin 看所有人的。
func (s *Store) ListSessions(ctx context.Context, u User, limit int) ([]Session, error) {
	q := sessionCols
	var args []any
	if !u.IsAdmin() {
		q += ` WHERE user_id = ?`
		args = append(args, u.ID)
	}
	q += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		ss, err := s.scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// DeleteSession 删除会话和它的消息、回报。running 时返回 ErrSessionBusy。
func (s *Store) DeleteSession(ctx context.Context, u User, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, id)
	if err != nil {
		return err
	}
	if ss.Status == SessionRunning {
		return ErrSessionBusy
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Messages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, messageCols+` WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SessionEvents(ctx context.Context, sessionID string) ([]SessionEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, message_id, daemon_id, kind, payload, created_at FROM session_events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionEvent{}
	for rows.Next() {
		var e SessionEvent
		var payload sql.NullString
		if err := rows.Scan(&e.ID, &e.SessionID, &e.MessageID, &e.DaemonID, &e.Kind, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		if payload.Valid {
			e.Payload = json.RawMessage(payload.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
