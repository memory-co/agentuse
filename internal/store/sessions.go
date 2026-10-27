package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	SessionIdle    = "idle"
	SessionPending = "pending"
	SessionRunning = "running"
	SessionWaiting = "waiting" // harness 在等用户回答提问，见 docs/v2/designs/question.md

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
	ID               string `json:"id"`
	UserID           string `json:"user_id"`
	CreatedBy        string `json:"created_by"`
	AgentID          string `json:"agent_id,omitempty"`
	Harness          string `json:"harness"`
	DaemonID         string `json:"daemon_id,omitempty"`
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
	// ErrSessionIdle 表示会话空闲，应该发下一条消息而不是插话。
	ErrSessionIdle = errors.New("会话空闲，直接发消息")
	// ErrSessionOrphaned 表示会话的 daemon 已删除，续不上了。
	ErrSessionOrphaned = errors.New("会话原来的 daemon 已删除，不能继续")
	// ErrRuntimeUnavailable 表示会话的运行时(daemon 上的 harness)没开启或不可用，派不出去。
	ErrRuntimeUnavailable = errors.New("harness 不可用")
)

// CreateSession 用一个 agent 建会话并写入第一条用户消息，会话为 pending。
// agent_id 由调用方指定，可以是任何人的 agent:会话属于 agent 的主人,created_by 记发起人;
// daemon_id 和 harness 取 agent 此刻的运行时，之后不再跟着 agent 变。
func (s *Store) CreateSession(ctx context.Context, u User, agentID, title string, content json.RawMessage) (Session, Message, error) {
	if !json.Valid(content) {
		return Session{}, Message{}, errors.New("input 必须是 JSON")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, Message{}, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id = ?`, agentID))
	if errors.Is(err, ErrNotFound) {
		return Session{}, Message{}, fmt.Errorf("agent %q 不存在", agentID)
	}
	if err != nil {
		return Session{}, Message{}, err
	}
	if a.DaemonID == "" {
		return Session{}, Message{}, fmt.Errorf("agent %q 没有运行时(它的 daemon 已删除),请先切换运行时", agentID)
	}
	if err := runtimeErr(ctx, tx, a.DaemonID, a.Harness); err != nil {
		return Session{}, Message{}, err
	}
	now := s.unix()
	ss := Session{ID: randomID("ss_"), UserID: a.UserID, CreatedBy: u.ID, AgentID: a.ID, Harness: a.Harness, DaemonID: a.DaemonID, Title: title, Status: SessionPending, CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_by, agent_id, harness, daemon_id, title, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ss.ID, ss.UserID, ss.CreatedBy, ss.AgentID, ss.Harness, ss.DaemonID, title, ss.Status, now, now)
	if err != nil {
		return Session{}, Message{}, err
	}
	m, err := insertMessage(ctx, tx, ss.ID, RoleUserMsg, content, now)
	if err != nil {
		return Session{}, Message{}, err
	}
	return ss, m, tx.Commit()
}

// AddMessage 在 idle 的会话里发下一条用户消息，会话回到 pending。会话忙时返回 ErrSessionBusy,改用 AddSteer。
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
	if err := runtimeErr(ctx, tx, ss.DaemonID, ss.Harness); err != nil {
		return Session{}, Message{}, err
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

// ClaimSession 给 daemon 挑一个 pending 的会话：会话定在这台 daemon 上(建会话时取 agent 的运行时)、
// harness 在这台可用且已开启;最早的优先。
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
		WHERE s.status = 'pending' AND s.daemon_id = ?
		  AND EXISTS (SELECT 1 FROM daemon_harnesses da
		              WHERE da.daemon_id = s.daemon_id AND da.harness = s.harness
		                AND da.available = 1 AND da.enabled = 1)
		ORDER BY s.updated_at, s.id LIMIT 1`, d.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Claimed{}, ErrNotFound
	}
	if err != nil {
		return Claimed{}, err
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET status = 'running', lease_until = ?, updated_at = ? WHERE id = ?`,
		now+leaseSeconds, now, id); err != nil {
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

// runningWhere 匹配「这台 daemon 正在回这条用户消息」的会话:running / waiting、归这台 daemon、消息是最后一条用户消息。
const runningWhere = `status IN ('running', 'waiting') AND daemon_id = ?
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
	_, err := s.ReportEvent(ctx, daemonID, messageID, kind, payload, leaseSeconds)
	return err
}

// ReportEvent 同 AddEvent,另外返回这次新建、要推给 daemon 的插话(提问超时时没送到的答案)。
func (s *Store) ReportEvent(ctx context.Context, daemonID string, messageID int64, kind string, payload json.RawMessage, leaseSeconds int64) ([]Steer, error) {
	switch kind {
	case EventProgress, EventPlan, EventSteered, EventQuestion, EventAnswered, EventQuestionExpired, EventDone, EventFailed:
	default:
		return nil, fmt.Errorf("未知回报类型 %q", kind)
	}
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	if !json.Valid(payload) {
		return nil, errors.New("payload 必须是 JSON")
	}
	var q struct {
		ToolUseID string          `json:"tool_use_id"`
		Questions json.RawMessage `json:"questions"`
	}
	_ = json.Unmarshal(payload, &q)
	if kind == EventQuestion && (q.ToolUseID == "" || len(q.Questions) == 0 || q.Questions[0] != '[') {
		return nil, errors.New("question 回报要带 tool_use_id 和 questions 数组")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.unix()
	var sessionID string
	finished := kind == EventDone || kind == EventFailed
	switch kind {
	case EventDone, EventFailed:
		var r struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(payload, &r)
		err = tx.QueryRowContext(ctx,
			`UPDATE sessions SET status = 'idle', lease_until = NULL, updated_at = ?,
			   harness_session_id = COALESCE(NULLIF(?, ''), harness_session_id)
			 WHERE `+runningWhere+` RETURNING id`,
			now, r.SessionID, daemonID, messageID, messageID).Scan(&sessionID)
	case EventQuestion, EventAnswered, EventQuestionExpired:
		status := SessionRunning
		if kind == EventQuestion {
			status = SessionWaiting
		}
		err = tx.QueryRowContext(ctx,
			`UPDATE sessions SET status = ?, lease_until = ?, updated_at = ? WHERE `+runningWhere+` RETURNING id`,
			status, now+leaseSeconds, now, daemonID, messageID, messageID).Scan(&sessionID)
	default: // 跑的过程中的回报：只续租
		err = tx.QueryRowContext(ctx,
			`UPDATE sessions SET lease_until = ? WHERE `+runningWhere+` RETURNING id`,
			now+leaseSeconds, daemonID, messageID, messageID).Scan(&sessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrStaleMessage
	}
	if err != nil {
		return nil, err
	}
	var steers []Steer
	switch kind {
	case EventSteered:
		var r struct {
			SteerID int64 `json:"steer_id"`
		}
		_ = json.Unmarshal(payload, &r)
		if _, err := tx.ExecContext(ctx,
			`UPDATE steers SET status = 'delivered', updated_at = ? WHERE id = ? AND message_id = ? AND status = 'pending'`,
			now, r.SteerID, messageID); err != nil {
			return nil, err
		}
	case EventQuestion:
		// 同一个 tool_use_id 报两次(重连后重发)只记一次
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO questions (session_id, message_id, tool_use_id, questions, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'pending', ?, ?) ON CONFLICT (message_id, tool_use_id) DO NOTHING`,
			sessionID, messageID, q.ToolUseID, string(q.Questions), now, now); err != nil {
			return nil, err
		}
	case EventAnswered:
		if _, err := tx.ExecContext(ctx,
			`UPDATE questions SET status = 'answered', updated_at = ? WHERE message_id = ? AND tool_use_id = ? AND status = 'pending'`,
			now, messageID, q.ToolUseID); err != nil {
			return nil, err
		}
	case EventQuestionExpired:
		if steers, err = expireQuestions(ctx, tx, `message_id = ? AND tool_use_id = ?`, now, messageID, q.ToolUseID); err != nil {
			return nil, err
		}
	}
	if finished {
		if _, err := insertMessage(ctx, tx, sessionID, RoleAssistantMsg, payload, now); err != nil {
			return nil, err
		}
		// 进程都结束了，还在等的提问作废;答了没送到的答案和插话一起转成下一条消息
		if _, err := expireQuestions(ctx, tx, `message_id = ?`, now, messageID); err != nil {
			return nil, err
		}
		if err := queueSteers(ctx, tx, sessionID, messageID, now); err != nil {
			return nil, err
		}
		steers = nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO session_events (session_id, message_id, daemon_id, kind, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, messageID, daemonID, kind, string(payload), now); err != nil {
		return nil, err
	}
	return steers, tx.Commit()
}

// RequeueExpired 把租约过期的会话放回 pending(还是给同一台 daemon),返回受影响的用户 id(用来叫醒他们的 daemon)。
func (s *Store) RequeueExpired(ctx context.Context) ([]string, error) {
	return s.requeue(ctx, `status IN ('running', 'waiting') AND lease_until < ?`, s.unix())
}

// RequeueDaemonSessions 把某台 daemon 名下 running、但最后一条用户消息不在 keep 里的会话放回 pending。
// daemon 重连时调用:它没在跑的就不该还是 running。
func (s *Store) RequeueDaemonSessions(ctx context.Context, daemonID string, keep []int64) ([]string, error) {
	where := `status IN ('running', 'waiting') AND daemon_id = ?`
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.unix()
	rows, err := tx.QueryContext(ctx,
		`UPDATE sessions SET status = 'pending', lease_until = NULL, updated_at = ?
		 WHERE `+where+` RETURNING id, user_id`, append([]any{now}, args...)...)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var users, ids []string
	for rows.Next() {
		var id, u string
		if err := rows.Scan(&id, &u); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		if !seen[u] {
			seen[u] = true
			users = append(users, u)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 进程没了，还在等的提问作废;答了没送到的答案转成插话，重新派出时推过去
	for _, id := range ids {
		if _, err := expireQuestions(ctx, tx, `session_id = ?`, now, id); err != nil {
			return nil, err
		}
	}
	return users, tx.Commit()
}

// failDaemonSessions 在删除 daemon 前调用：它名下还没回完的会话回到 idle,并追加一条失败的 assistant 消息。
func (s *Store) failDaemonSessions(ctx context.Context, tx *sql.Tx, daemonID string) error {
	return s.failSessions(ctx, tx, `daemon_id = ? AND status IN ('pending', 'running', 'waiting')`, "daemon 已删除", daemonID)
}

// failUnrunnable 把这台 daemon 名下排队中、但运行时已经派不出去(harness 关了或不可用)的会话判失败，
// 免得一直排队。只动 pending 的:已经在跑的由 daemon 跑完。
func (s *Store) failUnrunnable(ctx context.Context, tx *sql.Tx, daemonID string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT harness FROM sessions s WHERE daemon_id = ? AND status = 'pending'
		  AND NOT EXISTS (SELECT 1 FROM daemon_harnesses da
		                  WHERE da.daemon_id = s.daemon_id AND da.harness = s.harness
		                    AND da.available = 1 AND da.enabled = 1)`, daemonID)
	if err != nil {
		return err
	}
	var harnesses []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		harnesses = append(harnesses, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range harnesses {
		msg := runtimeErr(ctx, tx, daemonID, h)
		if msg == nil {
			continue
		}
		if !errors.Is(msg, ErrRuntimeUnavailable) {
			return msg
		}
		if err := s.failSessions(ctx, tx, `daemon_id = ? AND harness = ? AND status = 'pending'`, msg.Error(), daemonID, h); err != nil {
			return err
		}
	}
	return nil
}

// failSessions 让 where 选中的会话回到 idle,追加一条 {"ok":false,"error":reason} 的 assistant 消息，还在等的提问作废。
func (s *Store) failSessions(ctx context.Context, tx *sql.Tx, where, reason string, args ...any) error {
	now := s.unix()
	rows, err := tx.QueryContext(ctx,
		`UPDATE sessions SET status = 'idle', lease_until = NULL, updated_at = ?
		 WHERE `+where+` RETURNING id`, append([]any{now}, args...)...)
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
	content, err := json.Marshal(struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}{false, reason})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := insertMessage(ctx, tx, id, RoleAssistantMsg, content, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE questions SET status = 'expired', updated_at = ? WHERE session_id = ? AND status = 'pending'`, now, id); err != nil {
			return err
		}
	}
	return nil
}

const sessionCols = `SELECT id, user_id, created_by, agent_id, harness, daemon_id, harness_session_id, title, status, lease_until, created_at, updated_at FROM sessions`

func (s *Store) scanSession(row scanner) (Session, error) {
	var ss Session
	var agent, daemon, harness sql.NullString
	var lease sql.NullInt64
	err := row.Scan(&ss.ID, &ss.UserID, &ss.CreatedBy, &agent, &ss.Harness, &daemon, &harness, &ss.Title, &ss.Status, &lease, &ss.CreatedAt, &ss.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	ss.AgentID, ss.DaemonID, ss.HarnessSessionID = agent.String, daemon.String, harness.String
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
		query += ` AND (user_id = ? OR created_by = ?)`
		args = append(args, u.ID, u.ID)
	}
	return s.scanSession(q.QueryRowContext(ctx, query, args...))
}

func (s *Store) GetSession(ctx context.Context, u User, id string) (Session, error) {
	return s.getSession(ctx, s.db, u, id)
}

// ListSessions 列最近的会话：自己的 agent 上的、和自己发起的;admin 看所有人的。
func (s *Store) ListSessions(ctx context.Context, u User, limit int) ([]Session, error) {
	q := sessionCols
	var args []any
	if !u.IsAdmin() {
		q += ` WHERE user_id = ? OR created_by = ?`
		args = append(args, u.ID, u.ID)
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
	if ss.Status == SessionRunning || ss.Status == SessionWaiting {
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

// Steer 是一条插话，见 docs/v2/designs/steer.md。
type Steer struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	MessageID int64  `json:"message_id"`
	CreatedBy string `json:"created_by"`
	Prompt    string `json:"prompt"`
	Status    string `json:"status"` // pending / delivered / queued
	CreatedAt int64  `json:"created_at"`
}

const (
	SteerPending   = "pending"
	SteerDelivered = "delivered"
	SteerQueued    = "queued"
)

// AddSteer 在 pending / running / waiting 的会话里插话，挂在当时最后一条用户消息上。
// 会话 idle 时返回 ErrSessionIdle,改用 AddMessage。
func (s *Store) AddSteer(ctx context.Context, u User, sessionID, prompt string) (Session, Steer, error) {
	if strings.TrimSpace(prompt) == "" {
		return Session{}, Steer{}, errors.New("prompt 不能为空")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, Steer{}, err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, sessionID)
	if err != nil {
		return Session{}, Steer{}, err
	}
	if ss.Status == SessionIdle {
		return Session{}, Steer{}, ErrSessionIdle
	}
	if ss.DaemonID == "" {
		return Session{}, Steer{}, ErrSessionOrphaned
	}
	st := Steer{SessionID: ss.ID, CreatedBy: u.ID, Prompt: prompt, Status: SteerPending, CreatedAt: s.unix()}
	if err := tx.QueryRowContext(ctx, `SELECT MAX(id) FROM messages WHERE session_id = ? AND role = 'user'`, ss.ID).Scan(&st.MessageID); err != nil {
		return Session{}, Steer{}, err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO steers (session_id, message_id, created_by, prompt, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		st.SessionID, st.MessageID, st.CreatedBy, st.Prompt, st.Status, st.CreatedAt, st.CreatedAt)
	if err != nil {
		return Session{}, Steer{}, err
	}
	st.ID, _ = res.LastInsertId()
	return ss, st, tx.Commit()
}

// queueSteers:这一轮结束时还没送达的插话转成 queued,prompt 用空行拼成下一条用户消息，会话回到 pending。
func queueSteers(ctx context.Context, tx *sql.Tx, sessionID string, messageID, now int64) error {
	rows, err := tx.QueryContext(ctx,
		`UPDATE steers SET status = 'queued', updated_at = ? WHERE message_id = ? AND status = 'pending' RETURNING id, prompt`, now, messageID)
	if err != nil {
		return err
	}
	type row struct {
		id     int64
		prompt string
	}
	var queued []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.prompt); err != nil {
			rows.Close()
			return err
		}
		queued = append(queued, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(queued) == 0 {
		return err
	}
	// RETURNING 不保证顺序，按 id 排
	sort.Slice(queued, func(i, j int) bool { return queued[i].id < queued[j].id })
	prompts := make([]string, len(queued))
	for i, r := range queued {
		prompts[i] = r.prompt
	}
	content, _ := json.Marshal(map[string]string{"prompt": strings.Join(prompts, "\n\n")})
	if _, err := insertMessage(ctx, tx, sessionID, RoleUserMsg, content, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET status = 'pending', updated_at = ? WHERE id = ?`, now, sessionID)
	return err
}

const steerCols = `SELECT id, session_id, message_id, created_by, prompt, status, created_at FROM steers`

func (s *Store) querySteers(ctx context.Context, where string, args ...any) ([]Steer, error) {
	rows, err := s.db.QueryContext(ctx, steerCols+` WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Steer{}
	for rows.Next() {
		var st Steer
		if err := rows.Scan(&st.ID, &st.SessionID, &st.MessageID, &st.CreatedBy, &st.Prompt, &st.Status, &st.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Steers 列会话里的所有插话。
func (s *Store) Steers(ctx context.Context, sessionID string) ([]Steer, error) {
	return s.querySteers(ctx, `session_id = ?`, sessionID)
}

// PendingSteers 列挂在某条消息上、还没送达的插话(派出消息后推给 daemon)。
func (s *Store) PendingSteers(ctx context.Context, messageID int64) ([]Steer, error) {
	return s.querySteers(ctx, `message_id = ? AND status = 'pending'`, messageID)
}

// DaemonOfflineFor 说明排队中的会话是不是在等它的 daemon 上线:会话 pending、daemon 不在线时返回 true。
func (s *Store) DaemonOfflineFor(ctx context.Context, ss Session) (bool, error) {
	if ss.Status != SessionPending || ss.DaemonID == "" {
		return false, nil
	}
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM daemons WHERE id = ?`, ss.DaemonID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && status != DaemonOnline, err
}
