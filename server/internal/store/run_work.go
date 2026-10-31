package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) CreateMultiWork(ctx context.Context, u User, name, agentID, instructions string) (Work, Session, error) {
	name, err := cleanWorkName(name)
	if err != nil {
		return Work{}, Session{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, Session{}, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=? AND user_id=?`, agentID, u.ID))
	if err != nil {
		return Work{}, Session{}, err
	}
	if err = runRuntime(ctx, tx, a); err != nil {
		return Work{}, Session{}, err
	}
	now := s.unix()
	w := Work{ID: randomID("wk_"), UserID: u.ID, Name: name, Instructions: instructions, CoordinatorID: randomID("ss_"), CoordinatorAgentID: agentID, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, `INSERT INTO works(id,user_id,name,instructions,coordinator_id,coordinator_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, w.ID, u.ID, name, instructions, w.CoordinatorID, agentID, now, now); err != nil {
		return w, Session{}, err
	}
	ss, err := s.insertMulti(ctx, tx, u, w.ID, name)
	if err != nil {
		return w, ss, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE works SET coordinator_id=? WHERE id=?`, ss.ID, w.ID); err != nil {
		return w, ss, err
	}
	w.CoordinatorID = ss.ID
	ss.WorkRole = WorkCoordinator
	return w, ss, tx.Commit()
}
func (s *Store) WorkRunByToken(ctx context.Context, token string) (Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, runCols+` WHERE id=(SELECT run_id FROM work_run_tokens t WHERE t.token_hash=? AND t.attempt_id=session_runs.attempt_id) AND status IN ('running','waiting') AND lease_until>?`, HashSecret(token), s.unix()))
}
func (s *Store) runWorkToken(ctx context.Context, token string) (Work, User, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT t.work_id FROM work_run_tokens t JOIN session_runs r ON r.id=t.run_id WHERE t.token_hash=? AND t.attempt_id=r.attempt_id AND r.status IN ('running','waiting') AND r.lease_until>?`, HashSecret(token), s.unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
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
func (s *Store) notifyRunMessages(ctx context.Context, tx *sql.Tx, session string) error {
	ss, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, session)
	if err != nil {
		return err
	}
	if ss.WorkRole != WorkThread {
		return nil
	}
	ds, err := deliveries(ctx, tx, `session_id=?`, session)
	if err != nil {
		return err
	}
	groups := map[int64][]Delivery{}
	var ids []int64
	for _, d := range ds {
		if _, ok := groups[d.MessageID]; !ok {
			ids = append(ids, d.MessageID)
		}
		groups[d.MessageID] = append(groups[d.MessageID], d)
	}
	for _, id := range ids {
		ready := true
		for _, d := range groups[id] {
			if d.Status != "succeeded" && d.Status != "failed" && d.Status != "cancelled" {
				ready = false
			}
		}
		if !ready {
			continue
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO run_notifications VALUES(?) ON CONFLICT DO NOTHING`, id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		var summary strings.Builder
		for _, d := range groups[id] {
			fmt.Fprintf(&summary, "\n@%s: %s", d.AgentKey, d.Status)
			if d.RunID != "" {
				var raw sql.NullString
				if e = tx.QueryRowContext(ctx, `SELECT m.content FROM session_runs r LEFT JOIN messages m ON m.id=r.result_message_id WHERE r.id=?`, d.RunID).Scan(&raw); e != nil {
					return e
				}
				if raw.Valid {
					var v struct{ Output, Error string }
					_ = json.Unmarshal([]byte(raw.String), &v)
					text := v.Output
					if text == "" {
						text = v.Error
					}
					runes := []rune(text)
					if len(runes) > 4000 {
						runes = runes[:4000]
					}
					summary.WriteString("\n" + string(runes))
				}
			}
			if d.Error != "" {
				summary.WriteString("\n" + d.Error)
			}
		}
		wk, e := getWork(ctx, tx, User{Role: RoleAdmin}, ss.WorkID)
		if e != nil {
			return e
		}
		coord, e := s.getSession(ctx, tx, User{Role: RoleAdmin}, wk.CoordinatorID)
		if e != nil {
			return e
		}
		b, _ := json.Marshal(map[string]string{"prompt": fmt.Sprintf("[线程 %s「%s」回复了]\n%s", ss.ID, ss.Title, summary.String()), "from_session": ss.ID})
		a, e := s.runAgent(ctx, tx, User{ID: wk.UserID}, coord, wk.CoordinatorAgentID)
		if e == nil {
			e = runRuntime(ctx, tx, a)
		}
		if e != nil {
			if _, e = insertMessage(ctx, tx, coord.ID, RoleUserMsg, b, s.unix()); e != nil {
				return e
			}
			continue
		}
		if _, e = s.sendMultiTx(ctx, tx, User{ID: wk.UserID}, coord, b, []string{wk.CoordinatorAgentID}, "", true); e != nil {
			return e
		}
	}
	return nil
}
