package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/memory-co/agentuse/server/internal/store"
)

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
		store.InsertMessageInsert,
		sessionID, role, string(content), now)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
	return Message{ID: id, Seq: id, SessionID: sessionID, Role: role, Content: content, CreatedAt: now}, nil
}

func (s *Service) scanSession(row scanner) (Session, error) { return store.ScanSession(row) }

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Service) getSession(ctx context.Context, q querier, u User, id string) (Session, error) {
	query := sessionCols + store.GetSessionClause
	args := []any{id}
	if !u.IsAdmin() {
		query += store.GetSessionClause2
		args = append(args, u.ID, u.ID)
	}
	return s.scanSession(q.QueryRowContext(ctx, query, args...))
}

func (s *Service) GetSession(ctx context.Context, u User, id string) (Session, error) {
	return s.getSession(ctx, s.db, u, id)
}

// ListSessions 列最近的普通会话(不在工作里的):自己的 agent 上的、和自己发起的;admin 看所有人的。
func (s *Service) ListSessions(ctx context.Context, u User, limit int) ([]Session, error) {
	q := sessionCols + store.ListSessionsClause
	var args []any
	if !u.IsAdmin() {
		q += store.ListSessionsClause2
		args = append(args, u.ID, u.ID)
	}
	return s.listSessions(ctx, q, limit, args...)
}

// ListWorkSessions 列工作里的线程(不含协调者)。工作看不到返回 ErrNotFound。
func (s *Service) ListWorkSessions(ctx context.Context, u User, workID string, limit int) ([]Session, error) {
	w, err := getWork(ctx, s.db, u, workID)
	if err != nil {
		return nil, err
	}
	return s.listSessions(ctx, sessionCols+store.ListWorkSessionsClause, limit, w.ID, w.CoordinatorID)
}

func (s *Service) listSessions(ctx context.Context, q string, limit int, args ...any) ([]Session, error) {
	q += store.ListSessionsClause3
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
func (s *Service) DeleteSession(ctx context.Context, u User, id string) error {
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
	if _, err := tx.ExecContext(ctx, store.DeleteSessionDelete, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Messages(ctx context.Context, sessionID string) ([]Message, error) {
	return messages(ctx, s.db, sessionID)
}

func messages(ctx context.Context, q rowsQuerier, sessionID string) ([]Message, error) {
	rows, err := q.QueryContext(ctx, messageCols+store.MessagesClause, sessionID)
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
