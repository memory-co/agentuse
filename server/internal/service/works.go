package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/memory-co/agentuse/server/internal/store"
)

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

// getWork 取一个工作：普通用户只能取自己的，admin 能取所有人的。
func getWork(ctx context.Context, q querier, u User, id string) (Work, error) {
	query := workCols + store.GetWorkClause
	args := []any{id}
	if !u.IsAdmin() {
		query += store.GetWorkClause2
		args = append(args, u.ID)
	}
	return scanWork(q.QueryRowContext(ctx, query, args...))
}

func (s *Service) GetWork(ctx context.Context, u User, id string) (Work, error) {
	return getWork(ctx, s.db, u, id)
}

// ListWorks 列工作，最近有变化的在前。普通用户只看自己的,admin 看全部。
func (s *Service) ListWorks(ctx context.Context, u User) ([]Work, error) {
	q := workCols
	var args []any
	if !u.IsAdmin() {
		q += store.ListWorksClause
		args = append(args, u.ID)
	}
	rows, err := s.db.QueryContext(ctx, q+store.ListWorksClause2, args...)
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
func (s *Service) UpdateWork(ctx context.Context, u User, id string, p WorkPatch) (Work, error) {
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
		coord, e := s.getConversation(ctx, tx, u, w.CoordinatorID)
		if e != nil {
			return w, e
		}
		if coord.Status != "idle" && coord.Status != "resolved" {
			return w, ErrConversationBusy
		}
		a, e := scanAgent(tx.QueryRowContext(ctx, agentCols+store.UpdateWorkClause, *p.CoordinatorAgentID, w.UserID))
		if e != nil {
			return w, e
		}
		if e = runRuntime(ctx, tx, a); e != nil {
			return w, e
		}
		if _, e = tx.ExecContext(ctx, store.UpdateWorkUpdate, a.ID, w.ID); e != nil {
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
		if _, err := tx.ExecContext(ctx, store.UpdateWorkUpdate2, name, w.CoordinatorID); err != nil {
			return Work{}, err
		}
	}
	if p.Instructions != nil {
		w.Instructions = *p.Instructions
	}
	if _, err := tx.ExecContext(ctx, store.UpdateWorkUpdate3,
		w.Name, w.Instructions, now, w.ID); err != nil {
		return Work{}, err
	}
	w.UpdatedAt = max(w.UpdatedAt, now)
	err = tx.Commit()
	if err == nil {
		s.notifyResource("works", w.ID, w.UserID)
		s.notifyConversation(ctx, w.CoordinatorID)
		if failure := s.ReconcileWorkNotifications(ctx); failure != nil {
			s.log.Printf("work summary pending: %v", failure)
		}
	}
	return w, err
}

// DeleteWork 连协调者和所有线程一起删。有会话在跑时返回 ErrConversationBusy。
func (s *Service) DeleteWork(ctx context.Context, u User, id string) error {
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
		store.DeleteWorkSelect, w.ID).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return ErrConversationBusy
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM conversations WHERE work_id=?`, w.ID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = deleteConversationContents(ctx, tx, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, store.DeleteWorkDelete, w.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, store.DeleteWorkDelete2, w.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyResource("works", w.ID, w.UserID)
	for _, id := range ids {
		s.cleanupConversationFiles(ctx, id)
	}
	return nil
}

// ResolveConversation 把 idle 的会话标记为已完成;已经是已完成的直接返回。协调者不能标记。
func (s *Service) ResolveConversation(ctx context.Context, u User, id string) (Conversation, error) {
	return s.setResolved(ctx, u, id, ConversationIdle, ConversationResolved)
}

// ReopenConversation 取消已完成，回到 idle。
func (s *Service) ReopenConversation(ctx context.Context, u User, id string) (Conversation, error) {
	return s.setResolved(ctx, u, id, ConversationResolved, ConversationIdle)
}

func (s *Service) setResolved(ctx context.Context, u User, id, from, to string) (Conversation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()
	ss, err := s.getConversation(ctx, tx, u, id)
	if err != nil {
		return Conversation{}, err
	}
	if ss.WorkRole == WorkCoordinator {
		return Conversation{}, ErrCoordinator
	}
	if ss.Status == to {
		return ss, nil
	}
	if ss.Status != from {
		return Conversation{}, fmt.Errorf("%w:现在是 %s", ErrConversationState, ss.Status)
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx, store.SetResolvedUpdate, to, now, ss.ID); err != nil {
		return Conversation{}, err
	}
	ss.Status, ss.UpdatedAt = to, now
	err = tx.Commit()
	if err == nil {
		s.publishConversationChange(ss, "conversations")
	}
	return ss, err
}

// WorkByToken validates the server-scoped business credential for a delivered input.
func (s *Service) WorkByToken(ctx context.Context, token string) (Work, User, error) {
	return s.runWorkToken(ctx, token)
}
