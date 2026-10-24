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
	TaskPending = "pending"
	TaskRunning = "running"
	TaskDone    = "done"
	TaskFailed  = "failed"

	EventProgress = "progress"
	EventDone     = "done"
	EventFailed   = "failed"
)

type Task struct {
	ID         string          `json:"id"`
	UserID     string          `json:"user_id"`
	DaemonID   string          `json:"daemon_id,omitempty"`
	Agent      string          `json:"agent"`
	Status     string          `json:"status"`
	Input      json.RawMessage `json:"input"`
	Result     json.RawMessage `json:"result,omitempty"`
	LeaseUntil *int64          `json:"lease_until,omitempty"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
	FinishedAt *int64          `json:"finished_at,omitempty"`
}

type TaskEvent struct {
	ID        int64           `json:"id"`
	TaskID    string          `json:"task_id"`
	DaemonID  string          `json:"daemon_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

// ErrStaleTask 表示 daemon 回报的任务已经不归它了(租约过期被收回、或已结束)。
var ErrStaleTask = errors.New("store: task is not running on this daemon")

// CreateTask 建任务。agent 必须登记过。
func (s *Store) CreateTask(ctx context.Context, userID, agent string, input json.RawMessage) (Task, error) {
	if !json.Valid(input) {
		return Task{}, errors.New("input 必须是 JSON")
	}
	now := s.unix()
	t := Task{ID: randomID("tk_"), UserID: userID, Agent: agent, Status: TaskPending, Input: input, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, user_id, agent, status, input, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Agent, t.Status, string(t.Input), now, now)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return Task{}, fmt.Errorf("agent %q 没有登记", agent)
	}
	return t, err
}

// ClaimTask 给 daemon 挑一个任务：同一用户创建、agent 在这台 daemon 上可用且已开启，最早的优先。
// 没有可领的返回 ErrNotFound。
func (s *Store) ClaimTask(ctx context.Context, d Daemon, leaseSeconds int64) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `
		SELECT t.id FROM tasks t
		WHERE t.status = 'pending' AND t.user_id = ?
		  AND EXISTS (SELECT 1 FROM daemon_agents da
		              WHERE da.daemon_id = ? AND da.user_id = t.user_id AND da.agent = t.agent
		                AND da.available = 1 AND da.enabled = 1)
		ORDER BY t.created_at, t.id LIMIT 1`, d.UserID, d.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE tasks SET status = 'running', daemon_id = ?, lease_until = ?, updated_at = ? WHERE id = ?`,
		d.ID, now+leaseSeconds, now, id); err != nil {
		return Task{}, err
	}
	t, err := s.scanTask(tx.QueryRowContext(ctx, taskCols+` WHERE id = ?`, id))
	if err != nil {
		return Task{}, err
	}
	return t, tx.Commit()
}

// RenewLease 给 daemon 正在跑的任务续租。
func (s *Store) RenewLease(ctx context.Context, daemonID, taskID string, leaseSeconds int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET lease_until = ? WHERE id = ? AND daemon_id = ? AND status = 'running'`,
		s.unix()+leaseSeconds, taskID, daemonID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleTask
	}
	return nil
}

// AddEvent 记一条 daemon 回报，并续租;done / failed 时同时结束任务、写 result。
func (s *Store) AddEvent(ctx context.Context, daemonID, taskID, kind string, payload json.RawMessage, leaseSeconds int64) error {
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
	var res sql.Result
	if kind == EventProgress {
		res, err = tx.ExecContext(ctx,
			`UPDATE tasks SET lease_until = ? WHERE id = ? AND daemon_id = ? AND status = 'running'`,
			now+leaseSeconds, taskID, daemonID)
	} else {
		res, err = tx.ExecContext(ctx,
			`UPDATE tasks SET status = ?, result = ?, lease_until = NULL, updated_at = ?, finished_at = ?
			 WHERE id = ? AND daemon_id = ? AND status = 'running'`,
			kind, string(payload), now, now, taskID, daemonID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleTask
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO task_events (task_id, daemon_id, kind, payload, created_at) VALUES (?, ?, ?, ?, ?)`,
		taskID, daemonID, kind, string(payload), now); err != nil {
		return err
	}
	return tx.Commit()
}

// RequeueExpired 把租约过期的任务放回 pending,返回受影响任务的用户 id(用来叫醒他们的 daemon)。
func (s *Store) RequeueExpired(ctx context.Context) ([]string, error) {
	return s.requeue(ctx, `status = 'running' AND lease_until < ?`, s.unix())
}

// RequeueDaemonTasks 把某台 daemon 名下、不在 keep 里的 running 任务放回 pending。
// daemon 重连时调用:它没在跑的任务就不该还挂在它名下。
func (s *Store) RequeueDaemonTasks(ctx context.Context, daemonID string, keep []string) ([]string, error) {
	where := `status = 'running' AND daemon_id = ?`
	args := []any{daemonID}
	if len(keep) > 0 {
		where += ` AND id NOT IN (?` + strings.Repeat(`, ?`, len(keep)-1) + `)`
		for _, k := range keep {
			args = append(args, k)
		}
	}
	return s.requeue(ctx, where, args...)
}

func (s *Store) requeue(ctx context.Context, where string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`UPDATE tasks SET status = 'pending', daemon_id = NULL, lease_until = NULL, updated_at = ?
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

const taskCols = `SELECT id, user_id, daemon_id, agent, status, input, result, lease_until, created_at, updated_at, finished_at FROM tasks`

func (s *Store) scanTask(row scanner) (Task, error) {
	var t Task
	var daemon, result sql.NullString
	var input string
	var lease, finished sql.NullInt64
	err := row.Scan(&t.ID, &t.UserID, &daemon, &t.Agent, &t.Status, &input, &result, &lease, &t.CreatedAt, &t.UpdatedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	t.DaemonID, t.Input = daemon.String, json.RawMessage(input)
	if result.Valid {
		t.Result = json.RawMessage(result.String)
	}
	t.LeaseUntil, t.FinishedAt = nullInt(lease), nullInt(finished)
	return t, nil
}

func (s *Store) GetTask(ctx context.Context, u User, id string) (Task, error) {
	q := taskCols + ` WHERE id = ?`
	args := []any{id}
	if !u.IsAdmin() {
		q += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	return s.scanTask(s.db.QueryRowContext(ctx, q, args...))
}

// ListTasks 列最近的任务;admin 看所有人的。
func (s *Store) ListTasks(ctx context.Context, u User, limit int) ([]Task, error) {
	q := taskCols
	var args []any
	if !u.IsAdmin() {
		q += ` WHERE user_id = ?`
		args = append(args, u.ID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := s.scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TaskEvents(ctx context.Context, taskID string) ([]TaskEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, daemon_id, kind, payload, created_at FROM task_events WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		var payload sql.NullString
		if err := rows.Scan(&e.ID, &e.TaskID, &e.DaemonID, &e.Kind, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		if payload.Valid {
			e.Payload = json.RawMessage(payload.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
