package service

import (
	"context"
	"errors"
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
func (s *WorkService) getWork(ctx context.Context, q querier, u User, id string) (Work, error) {
	query := workCols + store.GetWorkClause
	args := []any{id}
	if !u.IsAdmin() {
		query += store.GetWorkClause2
		args = append(args, u.ID)
	}
	return scanWork(q.QueryRowContext(ctx, query, args...))
}

func (s *WorkService) GetWork(ctx context.Context, u User, id string) (Work, error) {
	return s.getWork(ctx, s.db, u, id)
}

// ListWorks 列工作，最近有变化的在前。普通用户只看自己的,admin 看全部。
func (s *WorkService) ListWorks(ctx context.Context, u User) ([]Work, error) {
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
func (s *WorkService) UpdateWork(ctx context.Context, u User, id string, p WorkPatch) (Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	w, err := s.getWork(ctx, tx, u, id)
	if err != nil {
		return Work{}, err
	}
	now := s.unix()
	if p.CoordinatorAgentID != nil {
		coord, e := s.conversations.getConversation(ctx, tx, u, w.CoordinatorID)
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
		if e = s.devices.runtimeErr(ctx, tx, a.DeviceID, a.Harness); e != nil {
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
		if err := s.conversations.renameConversation(ctx, tx, w.CoordinatorID, name); err != nil {
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
func (s *WorkService) DeleteWork(ctx context.Context, u User, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w, err := s.getWork(ctx, tx, u, id)
	if err != nil {
		return err
	}
	ids, err := s.conversations.deleteWorkConversations(ctx, tx, w.ID)
	if err != nil {
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
		s.conversations.cleanupConversationFiles(ctx, id)
	}
	return nil
}

func (s *WorkService) CreateWork(ctx context.Context, u User, name, agent, instructions string) (Work, Conversation, error) {
	name, err := cleanWorkName(name)
	if err != nil {
		return Work{}, Conversation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, Conversation{}, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=? AND user_id=?`, agent, u.ID))
	if err != nil {
		return Work{}, Conversation{}, err
	}
	if err = s.devices.runtimeErr(ctx, tx, a.DeviceID, a.Harness); err != nil {
		return Work{}, Conversation{}, err
	}
	now := s.unix()
	w := Work{ID: newUUID(), UserID: u.ID, Name: name, Instructions: instructions, CoordinatorID: newUUID(), CoordinatorAgentID: agent, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, store.CreateWorkInsert, w.ID, u.ID, name, instructions, w.CoordinatorID, agent, now, now); err != nil {
		return w, Conversation{}, err
	}
	ss, err := s.conversations.insertConversation(ctx, tx, u, w.ID, name)
	if err != nil {
		return w, ss, err
	}
	if _, err = tx.ExecContext(ctx, store.CreateWorkUpdate, ss.ID, w.ID); err != nil {
		return w, ss, err
	}
	w.CoordinatorID = ss.ID
	ss.WorkRole = WorkCoordinator
	err = tx.Commit()
	if err == nil {
		s.notifyResource("works", w.ID, w.UserID)
		s.publishConversationChange(ss, "conversations")
	}
	return w, ss, err
}
