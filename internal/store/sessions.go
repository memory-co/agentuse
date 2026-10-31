package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	SessionIdle    = "idle"
	SessionPending = "pending"
	SessionRunning = "running"
	SessionWaiting = "waiting" // harness 在等用户回答提问，见 docs/v2/designs/question.md
	// SessionResolved:已完成，见 docs/v2/designs/work.md。再发消息就重新打开。
	SessionResolved = "resolved"

	// 工作里的角色，见 docs/v2/designs/work.md
	WorkCoordinator = "coordinator"
	WorkThread      = "thread"

	RoleUserMsg      = "user"
	RoleAssistantMsg = "assistant"

	EventProgress = "progress"
	EventPlan     = "plan"
	EventSteered  = "steered"
	// 提问，见 docs/v2/designs/question.md
	EventQuestion        = "question"
	EventAnswered        = "answered"
	EventQuestionExpired = "question_expired"
	EventDone            = "done"
	EventFailed          = "failed"
)

type Session struct {
	Mode      string `json:"mode"`
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	CreatedBy string `json:"created_by"`
	WorkID    string `json:"work_id,omitempty"`
	WorkRole  string `json:"work_role,omitempty"` // coordinator / thread,不在工作里时为空
	Title     string `json:"title"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type Message struct {
	Seq            int64           `json:"seq"`
	RunID          string          `json:"run_id,omitempty"`
	AgentKey       string          `json:"agent_key,omitempty"`
	AgentName      string          `json:"agent_name,omitempty"`
	TargetAgentIDs []string        `json:"target_agent_ids,omitempty"`
	ID             int64           `json:"id"`
	SessionID      string          `json:"session_id"`
	Role           string          `json:"role"`
	Content        json.RawMessage `json:"content"`
	CreatedAt      int64           `json:"created_at"`
}

type SessionEvent struct {
	RunID     string          `json:"run_id,omitempty"`
	AttemptID string          `json:"attempt_id,omitempty"`
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	MessageID int64           `json:"message_id"`
	DaemonID  string          `json:"daemon_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

var (
	// ErrStaleMessage 表示 daemon 回报的消息已经不归它了(租约过期被收回、或已经回过)。
	ErrStaleMessage = errors.New("store: message is not running on this daemon")
	// ErrSessionBusy 表示会话还在等 harness 回复，不能再发消息。
	ErrSessionBusy = errors.New("会话还在等 harness 回复")
	// ErrSessionOrphaned 表示agent 的 daemon 已删除，不能执行。
	ErrSessionOrphaned = errors.New("agent 的 daemon 已删除，不能执行")
	// ErrRuntimeUnavailable 表示会话的运行时(daemon 上的 harness)没开启或不可用，派不出去。
	ErrRuntimeUnavailable = errors.New("harness 不可用")
	// ErrSessionState 表示会话当前的状态不能做这个操作(如标记完成时不是 idle)。
	ErrSessionState = errors.New("会话状态不对")
	// ErrCoordinator 表示这个操作不能对工作的协调者做。
	ErrCoordinator = errors.New("这是工作的协调者，不能单独操作，请操作整个工作")
)

func insertMessage(ctx context.Context, tx *sql.Tx, sessionID, role string, content json.RawMessage, now int64) (Message, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO messages (session_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, role, string(content), now)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
	return Message{ID: id, Seq: id, SessionID: sessionID, Role: role, Content: content, CreatedAt: now}, nil
}

// work_role 是算出来的：工作的 coordinator_id 指着它就是协调者，否则在工作里就是线程。
const sessionCols = `SELECT id,user_id,created_by,work_id,
 CASE WHEN work_id IS NULL THEN '' WHEN EXISTS(SELECT 1 FROM works w WHERE w.coordinator_id=sessions.id) THEN 'coordinator' ELSE 'thread' END,
 title,status,created_at,updated_at,mode FROM sessions`

func (s *Store) scanSession(row scanner) (Session, error) {
	var ss Session
	var work sql.NullString
	err := row.Scan(&ss.ID, &ss.UserID, &ss.CreatedBy, &work, &ss.WorkRole, &ss.Title, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt, &ss.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	ss.WorkID = work.String
	return ss, err
}

const messageCols = `SELECT id, session_id, role, content, created_at,
 COALESCE((SELECT run_id FROM message_authors a WHERE a.message_id=messages.id),''),
 COALESCE((SELECT agent_key FROM message_authors a WHERE a.message_id=messages.id),''),
 COALESCE((SELECT agent_name FROM message_authors a WHERE a.message_id=messages.id),''),
 (SELECT json_group_array(agent_key) FROM message_deliveries d WHERE d.message_id=messages.id) FROM messages`

func scanMessage(row scanner) (Message, error) {
	var m Message
	var content, targets string
	err := row.Scan(&m.ID, &m.SessionID, &m.Role, &content, &m.CreatedAt, &m.RunID, &m.AgentKey, &m.AgentName, &targets)
	m.Seq = m.ID
	_ = json.Unmarshal([]byte(targets), &m.TargetAgentIDs)
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
		query += ` AND (user_id = ? OR created_by = ?)`
		args = append(args, u.ID, u.ID)
	}
	return s.scanSession(q.QueryRowContext(ctx, query, args...))
}

func (s *Store) GetSession(ctx context.Context, u User, id string) (Session, error) {
	return s.getSession(ctx, s.db, u, id)
}

// ListSessions 列最近的普通会话(不在工作里的):自己的 agent 上的、和自己发起的;admin 看所有人的。
func (s *Store) ListSessions(ctx context.Context, u User, limit int) ([]Session, error) {
	q := sessionCols + ` WHERE work_id IS NULL`
	var args []any
	if !u.IsAdmin() {
		q += ` AND (user_id = ? OR created_by = ?)`
		args = append(args, u.ID, u.ID)
	}
	return s.listSessions(ctx, q, limit, args...)
}

// ListWorkSessions 列工作里的线程(不含协调者)。工作看不到返回 ErrNotFound。
func (s *Store) ListWorkSessions(ctx context.Context, u User, workID string, limit int) ([]Session, error) {
	w, err := getWork(ctx, s.db, u, workID)
	if err != nil {
		return nil, err
	}
	return s.listSessions(ctx, sessionCols+` WHERE work_id = ? AND id != ?`, limit, w.ID, w.CoordinatorID)
}

func (s *Store) listSessions(ctx context.Context, q string, limit int, args ...any) ([]Session, error) {
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

// DeleteSession 删除会话和它的消息、回报。running / waiting 时返回 ErrSessionBusy。
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
	if ss.Status == SessionRunning || ss.Status == SessionWaiting || ss.Status == SessionPending {
		return ErrSessionBusy
	}
	if ss.WorkRole == WorkCoordinator {
		return ErrCoordinator
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

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
