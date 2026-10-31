package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Work 是一个工作：协调者会话 + 若干线程会话 + 一份共用说明，见 docs/v2/designs/work.md。
type Work struct {
	CoordinatorAgentID string `json:"coordinator_agent_id,omitempty"`
	ID                 string `json:"id"`
	UserID             string `json:"user_id"`
	Name               string `json:"name"`
	Instructions       string `json:"instructions"`
	CoordinatorID      string `json:"coordinator_id"`
	ThreadCount        int    `json:"thread_count"`
	CreatedAt          int64  `json:"created_at"`
	// UpdatedAt 取工作本身和里面会话最近一次变化里晚的那个。
	UpdatedAt int64 `json:"updated_at"`
}

// WorkPatch 是修改工作时要改的字段，nil 表示不改。
type WorkPatch struct {
	CoordinatorAgentID *string
	Name               *string
	Instructions       *string
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
   , COALESCE(w.coordinator_agent_id,'') FROM works w`

func scanWork(row scanner) (Work, error) {
	var w Work
	err := row.Scan(&w.ID, &w.UserID, &w.Name, &w.Instructions, &w.CoordinatorID, &w.ThreadCount, &w.CreatedAt, &w.UpdatedAt, &w.CoordinatorAgentID)
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
	if p.CoordinatorAgentID != nil {
		coord, e := s.getSession(ctx, tx, u, w.CoordinatorID)
		if e != nil {
			return w, e
		}
		if coord.Status != "idle" && coord.Status != "resolved" {
			return w, ErrSessionBusy
		}
		a, e := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=? AND user_id=?`, *p.CoordinatorAgentID, w.UserID))
		if e != nil {
			return w, e
		}
		if e = runRuntime(ctx, tx, a); e != nil {
			return w, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE works SET coordinator_agent_id=? WHERE id=?`, a.ID, w.ID); e != nil {
			return w, e
		}
		w.CoordinatorAgentID = a.ID
	}
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
		`SELECT COUNT(*) FROM sessions WHERE work_id = ? AND (status IN ('running', 'waiting') OR (mode='multi_agent' AND status='pending'))`, w.ID).Scan(&busy); err != nil {
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

// WorkByToken validates a coordinator's current run and attempt.
func (s *Store) WorkByToken(ctx context.Context, token string) (Work, User, error) {
	return s.runWorkToken(ctx, token)
}
