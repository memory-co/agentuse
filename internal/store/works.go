package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Work 是一个工作：协调者会话 + 若干线程会话 + 一份共用说明，见 docs/v2/designs/work.md。
type Work struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	Name          string `json:"name"`
	Instructions  string `json:"instructions"`
	CoordinatorID string `json:"coordinator_id"`
	ThreadCount   int    `json:"thread_count"`
	CreatedAt     int64  `json:"created_at"`
	// UpdatedAt 取工作本身和里面会话最近一次变化里晚的那个。
	UpdatedAt int64 `json:"updated_at"`
}

// WorkPatch 是修改工作时要改的字段，nil 表示不改。
type WorkPatch struct {
	Name         *string
	Instructions *string
}

func cleanWorkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("工作名字必填")
	}
	if utf8.RuneCountInString(name) > 64 {
		return "", errors.New("工作名字最长 64 个字")
	}
	return name, nil
}

const workCols = `SELECT w.id, w.user_id, w.name, w.instructions, w.coordinator_id,
  (SELECT COUNT(*) FROM sessions s WHERE s.work_id = w.id AND s.id != w.coordinator_id),
  w.created_at,
  MAX(w.updated_at, COALESCE((SELECT MAX(s.updated_at) FROM sessions s WHERE s.work_id = w.id), 0)) AS last
  FROM works w`

func scanWork(row scanner) (Work, error) {
	var w Work
	err := row.Scan(&w.ID, &w.UserID, &w.Name, &w.Instructions, &w.CoordinatorID, &w.ThreadCount, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, ErrNotFound
	}
	return w, err
}

// getWork 取一个工作：普通用户只能取自己的，admin 能取所有人的。
func getWork(ctx context.Context, q querier, u User, id string) (Work, error) {
	query := workCols + ` WHERE w.id = ?`
	args := []any{id}
	if !u.IsAdmin() {
		query += ` AND w.user_id = ?`
		args = append(args, u.ID)
	}
	return scanWork(q.QueryRowContext(ctx, query, args...))
}

// CreateWork 建工作，同时用 agentID 建出协调者会话(idle,还没有消息)。
func (s *Store) CreateWork(ctx context.Context, u User, name, agentID, instructions string) (Work, Session, error) {
	name, err := cleanWorkName(name)
	if err != nil {
		return Work{}, Session{}, err
	}
	if agentID == "" {
		return Work{}, Session{}, errors.New("agent_id 必填(协调者用哪个 agent)")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, Session{}, err
	}
	defer tx.Rollback()
	w := Work{ID: randomID("wk_"), UserID: u.ID, Name: name, Instructions: instructions, CreatedAt: s.unix()}
	w.UpdatedAt = w.CreatedAt
	// 先建工作再建协调者：会话的 work_id 要指着它
	coord := randomID("ss_")
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO works (id, user_id, name, instructions, coordinator_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.UserID, w.Name, w.Instructions, coord, w.CreatedAt, w.UpdatedAt); err != nil {
		return Work{}, Session{}, err
	}
	ss, err := s.insertSession(ctx, tx, u, w.ID, agentID, name, SessionIdle)
	if err != nil {
		return Work{}, Session{}, err
	}
	if ss.UserID != u.ID { // 工作里只用自己的 agent,协调者和线程一样
		return Work{}, Session{}, fmt.Errorf("协调者只能用自己的 agent: %s", agentID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE works SET coordinator_id = ? WHERE id = ?`, ss.ID, w.ID); err != nil {
		return Work{}, Session{}, err
	}
	w.CoordinatorID, ss.WorkRole = ss.ID, WorkCoordinator
	return w, ss, tx.Commit()
}

func (s *Store) GetWork(ctx context.Context, u User, id string) (Work, error) {
	return getWork(ctx, s.db, u, id)
}

// ListWorks 列工作，最近有变化的在前。普通用户只看自己的,admin 看全部。
func (s *Store) ListWorks(ctx context.Context, u User) ([]Work, error) {
	q := workCols
	var args []any
	if !u.IsAdmin() {
		q += ` WHERE w.user_id = ?`
		args = append(args, u.ID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY last DESC, w.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Work{}
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateWork 改名字、说明;协调者会话的标题跟着名字走。
func (s *Store) UpdateWork(ctx context.Context, u User, id string, p WorkPatch) (Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	w, err := getWork(ctx, tx, u, id)
	if err != nil {
		return Work{}, err
	}
	now := s.unix()
	if p.Name != nil {
		name, err := cleanWorkName(*p.Name)
		if err != nil {
			return Work{}, err
		}
		w.Name = name
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET title = ? WHERE id = ?`, name, w.CoordinatorID); err != nil {
			return Work{}, err
		}
	}
	if p.Instructions != nil {
		w.Instructions = *p.Instructions
	}
	if _, err := tx.ExecContext(ctx, `UPDATE works SET name = ?, instructions = ?, updated_at = ? WHERE id = ?`,
		w.Name, w.Instructions, now, w.ID); err != nil {
		return Work{}, err
	}
	w.UpdatedAt = max(w.UpdatedAt, now)
	return w, tx.Commit()
}

// DeleteWork 连协调者和所有线程一起删。有会话在跑时返回 ErrSessionBusy。
func (s *Store) DeleteWork(ctx context.Context, u User, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w, err := getWork(ctx, tx, u, id)
	if err != nil {
		return err
	}
	var busy int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE work_id = ? AND status IN ('running', 'waiting')`, w.ID).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return ErrSessionBusy
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE work_id = ?`, w.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM works WHERE id = ?`, w.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveSession 把 idle 的会话标记为已完成;已经是已完成的直接返回。协调者不能标记。
func (s *Store) ResolveSession(ctx context.Context, u User, id string) (Session, error) {
	return s.setResolved(ctx, u, id, SessionIdle, SessionResolved)
}

// ReopenSession 取消已完成，回到 idle。
func (s *Store) ReopenSession(ctx context.Context, u User, id string) (Session, error) {
	return s.setResolved(ctx, u, id, SessionResolved, SessionIdle)
}

func (s *Store) setResolved(ctx context.Context, u User, id, from, to string) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, id)
	if err != nil {
		return Session{}, err
	}
	if ss.WorkRole == WorkCoordinator {
		return Session{}, ErrCoordinator
	}
	if ss.Status == to {
		return ss, nil
	}
	if ss.Status != from {
		return Session{}, fmt.Errorf("%w:现在是 %s", ErrSessionState, ss.Status)
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`, to, now, ss.ID); err != nil {
		return Session{}, err
	}
	ss.Status, ss.UpdatedAt = to, now
	return ss, tx.Commit()
}

// maxNoteRunes 是线程回复转给协调者时最多带的字数。
const maxNoteRunes = 4000

// notifyCoordinator 在线程回完一条后，把结果发给工作的协调者：协调者空闲(或已完成)时是下一条消息，
// 在排队 / 在跑时是一条插话。sessionID 不是线程时什么都不做。
func (s *Store) notifyCoordinator(ctx context.Context, tx *sql.Tx, sessionID string, payload json.RawMessage, now int64) (*CoordinatorNote, error) {
	thread, err := s.scanSession(tx.QueryRowContext(ctx, sessionCols+` WHERE id = ?`, sessionID))
	if err != nil {
		return nil, err
	}
	if thread.WorkRole != WorkThread {
		return nil, nil
	}
	var coordID, owner string
	if err := tx.QueryRowContext(ctx, `SELECT coordinator_id, user_id FROM works WHERE id = ?`, thread.WorkID).Scan(&coordID, &owner); err != nil {
		return nil, err
	}
	coord, err := s.scanSession(tx.QueryRowContext(ctx, sessionCols+` WHERE id = ?`, coordID))
	if err != nil {
		return nil, err
	}
	if coord.DaemonID == "" { // 协调者的 daemon 被删了，续不上
		return nil, nil
	}
	var r struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal(payload, &r)
	body := r.Output
	if !r.OK {
		body = "失败: " + r.Error
		if r.Output != "" {
			body += "\n" + r.Output
		}
	}
	if rs := []rune(body); len(rs) > maxNoteRunes {
		body = string(rs[:maxNoteRunes]) + "…"
	}
	prompt := fmt.Sprintf("[线程 %s「%s」回复了]\n%s", thread.ID, thread.Title, body)
	note := &CoordinatorNote{Session: coord}
	switch coord.Status {
	case SessionIdle, SessionResolved:
		content, _ := json.Marshal(map[string]string{"prompt": prompt, "from_session": thread.ID})
		if _, err := insertMessage(ctx, tx, coord.ID, RoleUserMsg, content, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = 'pending', updated_at = ? WHERE id = ?`, now, coord.ID); err != nil {
			return nil, err
		}
		note.Session.Status, note.Session.UpdatedAt = SessionPending, now
	default:
		st := Steer{SessionID: coord.ID, CreatedBy: owner, Prompt: prompt, Status: SteerPending, CreatedAt: now}
		if err := tx.QueryRowContext(ctx, `SELECT MAX(id) FROM messages WHERE session_id = ? AND role = 'user'`, coord.ID).Scan(&st.MessageID); err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO steers (session_id, message_id, created_by, prompt, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			st.SessionID, st.MessageID, st.CreatedBy, st.Prompt, st.Status, now, now)
		if err != nil {
			return nil, err
		}
		st.ID, _ = res.LastInsertId()
		note.Steer = &st
	}
	return note, nil
}

// WorkByToken 用协调者跑时拿到的工作令牌(wt_)找工作和它的主人。
// 令牌只在发它的那条消息还在跑(running / waiting、还是协调者最后一条用户消息)时有效。
func (s *Store) WorkByToken(ctx context.Context, token string) (Work, User, error) {
	var workID string
	err := s.db.QueryRowContext(ctx, `
		SELECT t.work_id FROM work_tokens t
		JOIN works w ON w.id = t.work_id
		JOIN sessions s ON s.id = w.coordinator_id
		WHERE t.token_hash = ? AND s.status IN ('running', 'waiting')
		  AND t.message_id = (SELECT MAX(m.id) FROM messages m WHERE m.session_id = s.id AND m.role = 'user')`,
		HashSecret(token)).Scan(&workID)
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, User{}, ErrNotFound
	}
	if err != nil {
		return Work{}, User{}, err
	}
	w, err := scanWork(s.db.QueryRowContext(ctx, workCols+` WHERE w.id = ?`, workID))
	if err != nil {
		return Work{}, User{}, err
	}
	u, err := s.GetUser(ctx, w.UserID)
	return w, u, err
}
