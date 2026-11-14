package work

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
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
func (s *Service) LookupWork(ctx context.Context, q core.Querier, u core.User, id string) (core.Work, error) {
	query := core.WorkCols + store.GetWorkClause
	args := []any{id}
	if !u.IsAdmin() {
		query += store.GetWorkClause2
		args = append(args, u.ID)
	}
	return core.ScanWork(q.QueryRowContext(ctx, query, args...))
}

func (s *Service) GetWork(ctx context.Context, u core.User, id string) (core.Work, error) {
	return s.LookupWork(ctx, s.DB, u, id)
}

// ListWorks 列工作，最近有变化的在前。普通用户只看自己的,admin 看全部。
func (s *Service) ListWorks(ctx context.Context, u core.User) ([]core.Work, error) {
	q := core.WorkCols
	var args []any
	if !u.IsAdmin() {
		q += store.ListWorksClause
		args = append(args, u.ID)
	}
	rows, err := s.DB.QueryContext(ctx, q+store.ListWorksClause2, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Work{}
	for rows.Next() {
		w, err := core.ScanWork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateWork 改名字、说明;协调者会话的标题跟着名字走。
func (s *Service) UpdateWork(ctx context.Context, u core.User, id string, p core.WorkPatch) (core.Work, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return core.Work{}, err
	}
	defer tx.Rollback()
	w, err := s.LookupWork(ctx, tx, u, id)
	if err != nil {
		return core.Work{}, err
	}
	now := s.Unix()
	if p.CoordinatorAgentID != nil {
		coord, e := s.ConversationService.LookupConversation(ctx, tx, u, w.CoordinatorID)
		if e != nil {
			return w, e
		}
		if coord.Status != "idle" && coord.Status != "resolved" {
			return w, core.ErrConversationBusy
		}
		a, e := core.ScanAgent(tx.QueryRowContext(ctx, core.AgentCols+store.UpdateWorkClause, *p.CoordinatorAgentID, w.UserID))
		if e != nil {
			return w, e
		}
		if e = s.DeviceService.CheckRuntime(ctx, tx, a.DeviceID, a.Harness); e != nil {
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
			return core.Work{}, err
		}
		w.Name = name
		if err := s.ConversationService.RenameConversation(ctx, tx, w.CoordinatorID, name); err != nil {
			return core.Work{}, err
		}
	}
	if p.Instructions != nil {
		w.Instructions = *p.Instructions
	}
	if _, err := tx.ExecContext(ctx, store.UpdateWorkUpdate3,
		w.Name, w.Instructions, now, w.ID); err != nil {
		return core.Work{}, err
	}
	w.UpdatedAt = max(w.UpdatedAt, now)
	err = tx.Commit()
	if err == nil {
		s.NotifyResource("works", w.ID, w.UserID)
		s.NotifyConversation(ctx, w.CoordinatorID)
		if failure := s.ReconcileWorkNotifications(ctx); failure != nil {
			s.Log.Printf("work summary pending: %v", failure)
		}
	}
	return w, err
}

// DeleteWork 连协调者和所有线程一起删。有会话在跑时返回 ErrConversationBusy。
func (s *Service) DeleteWork(ctx context.Context, u core.User, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w, err := s.LookupWork(ctx, tx, u, id)
	if err != nil {
		return err
	}
	ids, err := s.ConversationService.DeleteWorkConversations(ctx, tx, w.ID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, store.DeleteWorkDelete2, w.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.NotifyResource("works", w.ID, w.UserID)
	for _, id := range ids {
		s.ConversationService.CleanupFiles(ctx, id)
	}
	return nil
}

func (s *Service) CreateWork(ctx context.Context, u core.User, name, agent, instructions string) (core.Work, core.Conversation, error) {
	name, err := cleanWorkName(name)
	if err != nil {
		return core.Work{}, core.Conversation{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return core.Work{}, core.Conversation{}, err
	}
	defer tx.Rollback()
	a, err := core.ScanAgent(tx.QueryRowContext(ctx, core.AgentCols+` WHERE id=? AND user_id=?`, agent, u.ID))
	if err != nil {
		return core.Work{}, core.Conversation{}, err
	}
	if err = s.DeviceService.CheckRuntime(ctx, tx, a.DeviceID, a.Harness); err != nil {
		return core.Work{}, core.Conversation{}, err
	}
	now := s.Unix()
	w := core.Work{ID: core.NewUUID(), UserID: u.ID, Name: name, Instructions: instructions, CoordinatorID: core.NewUUID(), CoordinatorAgentID: agent, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, store.CreateWorkInsert, w.ID, u.ID, name, instructions, w.CoordinatorID, agent, now, now); err != nil {
		return w, core.Conversation{}, err
	}
	ss, err := s.ConversationService.InsertConversation(ctx, tx, u, w.ID, name)
	if err != nil {
		return w, ss, err
	}
	if _, err = tx.ExecContext(ctx, store.CreateWorkUpdate, ss.ID, w.ID); err != nil {
		return w, ss, err
	}
	w.CoordinatorID = ss.ID
	ss.WorkRole = core.WorkCoordinator
	err = tx.Commit()
	if err == nil {
		s.NotifyResource("works", w.ID, w.UserID)
		s.PublishConversationChange(ss, "conversations")
	}
	return w, ss, err
}
