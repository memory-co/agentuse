package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) CreateMultiWork(ctx context.Context, u User, name, agent, instructions string) (Work, Session, error) {
	name, err := cleanWorkName(name)
	if err != nil {
		return Work{}, Session{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, Session{}, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=? AND user_id=?`, agent, u.ID))
	if err != nil {
		return Work{}, Session{}, err
	}
	if err = runRuntime(ctx, tx, a); err != nil {
		return Work{}, Session{}, err
	}
	now := s.unix()
	w := Work{ID: newUUID(), UserID: u.ID, Name: name, Instructions: instructions, CoordinatorID: newUUID(), CoordinatorAgentID: agent, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, store.CreateMultiWorkInsert, w.ID, u.ID, name, instructions, w.CoordinatorID, agent, now, now); err != nil {
		return w, Session{}, err
	}
	ss, err := s.insertMulti(ctx, tx, u, w.ID, name)
	if err != nil {
		return w, ss, err
	}
	if _, err = tx.ExecContext(ctx, store.CreateMultiWorkUpdate, ss.ID, w.ID); err != nil {
		return w, ss, err
	}
	w.CoordinatorID = ss.ID
	ss.WorkRole = WorkCoordinator
	err = tx.Commit()
	if err == nil {
		s.notifyResource("works", w.ID, w.UserID)
		s.notifySession(ss, "sessions")
	}
	return w, ss, err
}
func (s *Service) runWorkToken(ctx context.Context, token string) (Work, User, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT t.work_id FROM work_run_tokens t JOIN message_agents p ON p.message_id=t.message_id AND p.agent_id=t.agent_id WHERE t.token_hash=? AND p.status IN ('sent','accepted')`, HashSecret(token)).Scan(&id)
	if err == sql.ErrNoRows {
		return Work{}, User{}, ErrNotFound
	}
	if err != nil {
		return Work{}, User{}, err
	}
	w, err := scanWork(s.db.QueryRowContext(ctx, workCols+` WHERE w.id=?`, id))
	if err != nil {
		return w, User{}, err
	}
	u, err := s.GetUser(ctx, w.UserID)
	return w, u, err
}
func (s *Service) WorkRunByToken(ctx context.Context, token string) (Run, error) {
	return scanNativeRun(s.db.QueryRowContext(ctx, nativeRunCols+` WHERE id=(SELECT p.run_id FROM work_run_tokens t JOIN message_agents p ON p.message_id=t.message_id AND p.agent_id=t.agent_id WHERE t.token_hash=? AND p.status IN ('sent','accepted'))`, HashSecret(token)))
}

func (s *Service) notifyRunMessages(ctx context.Context, tx *sql.Tx, conversation string) error {
	ss, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, conversation)
	if err != nil {
		return err
	}
	if ss.WorkRole != WorkThread {
		return nil
	}
	prompts, err := deliveries(ctx, tx, `session_id=?`, conversation)
	if err != nil {
		return err
	}
	groups := map[string][]Delivery{}
	for _, p := range prompts {
		groups[p.MessageID] = append(groups[p.MessageID], p)
	}
	for id, group := range groups {
		done := true
		for _, p := range group {
			if p.Status != "completed" && p.Status != "failed" && p.Status != "cancelled" && p.Status != "rejected" {
				done = false
			}
		}
		if !done {
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO run_notifications(message_id) VALUES(?)`, id)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			continue
		}
		var summary strings.Builder
		for _, p := range group {
			fmt.Fprintf(&summary, "\n@%s: %s", p.AgentID, p.Status)
			if p.Error != "" {
				summary.WriteString("\n" + p.Error)
			}
			if p.RunID != "" {
				var output string
				if err = tx.QueryRowContext(ctx, `SELECT output FROM runs WHERE id=?`, p.RunID).Scan(&output); err != nil {
					return err
				}
				summary.WriteString("\n" + output)
			}
		}
		w, err := getWork(ctx, tx, User{Role: RoleAdmin}, ss.WorkID)
		if err != nil {
			return err
		}
		coord, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, w.CoordinatorID)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"prompt": fmt.Sprintf("[Thread %s (%s) replied]\n%s", ss.ID, ss.Title, summary.String())})
		targets := []string{}
		if w.CoordinatorAgentID != "" {
			targets = append(targets, w.CoordinatorAgentID)
		}
		if _, err = s.sendMultiTx(ctx, tx, User{ID: w.UserID}, coord, payload, targets, nil, nil); err != nil {
			return err
		}
	}
	return nil
}
