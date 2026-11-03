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
	return w, ss, tx.Commit()
}
func (s *Service) runWorkToken(ctx context.Context, token string) (Work, User, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT t.work_id FROM work_run_tokens t JOIN run_prompts p ON p.id=t.prompt_id WHERE t.token_hash=? AND p.status IN ('sent','accepted')`, HashSecret(token)).Scan(&id)
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
	return scanNativeRun(s.db.QueryRowContext(ctx, nativeRunCols+` WHERE id=(SELECT p.run_id FROM work_run_tokens t JOIN run_prompts p ON p.id=t.prompt_id WHERE t.token_hash=? AND p.status IN ('sent','accepted'))`, HashSecret(token)))
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
	groups := map[int64][]Delivery{}
	for _, p := range prompts {
		groups[p.MessageID] = append(groups[p.MessageID], p)
	}
	for id, group := range groups {
		done := true
		for _, p := range group {
			if p.Status != "completed" && p.Status != "failed" && p.Status != "cancelled" {
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
		payload, _ := json.Marshal(map[string]string{"prompt": fmt.Sprintf("[线程 %s「%s」回复了]\n%s", ss.ID, ss.Title, summary.String())})
		if _, err = s.sendMultiTx(ctx, tx, User{ID: w.UserID}, coord, payload, []string{w.CoordinatorAgentID}, ""); err != nil {
			return err
		}
	}
	return nil
}
