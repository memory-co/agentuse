package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func promptContent(content json.RawMessage) bool {
	var v map[string]any
	if json.Unmarshal(content, &v) != nil {
		return false
	}
	p, ok := v["prompt"].(string)
	return ok && strings.TrimSpace(p) != "" && len(v) == 1
}
func (s *Store) CheckRun(ctx context.Context, q querier, daemon, run, attempt string) (Run, error) {
	r, err := scanRun(q.QueryRowContext(ctx, runCols+` WHERE id=?`, run))
	if err != nil {
		return r, err
	}
	if r.DaemonID != daemon {
		return Run{}, ErrNotFound
	}
	if r.AttemptID != attempt || r.LeaseUntil == nil || *r.LeaseUntil <= s.unix() || (r.Status != "running" && r.Status != "waiting") {
		return Run{}, ErrStaleMessage
	}
	return r, nil
}
func (s *Store) RunAccess(ctx context.Context, daemon, session, run, attempt string) (Run, error) {
	r, e := s.CheckRun(ctx, s.db, daemon, run, attempt)
	if e == nil && r.SessionID != session {
		return Run{}, ErrNotFound
	}
	return r, e
}
func (s *Store) RenewRun(ctx context.Context, daemon, run, attempt string, lease int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.CheckRun(ctx, tx, daemon, run, attempt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE session_runs SET lease_until=? WHERE id=?`, s.unix()+lease, run); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) runInputs(ctx context.Context, tx *sql.Tx, r Run) ([]Message, []int64, error) {
	ds, err := deliveries(ctx, tx, `run_id=? AND status IN ('pending','delivered')`, r.ID)
	if err != nil {
		return nil, nil, err
	}
	tasks := []int64{}
	blocked := false
	var primaryPrompt bool
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT is_prompt FROM message_formats WHERE message_id=?),0)`, r.MessageID).Scan(&primaryPrompt); err != nil {
		return nil, nil, err
	}
	for _, d := range ds {
		var isPrompt bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT is_prompt FROM message_formats WHERE message_id=?),0)`, d.MessageID).Scan(&isPrompt); err != nil {
			return nil, nil, err
		}
		include := d.Kind == "new_run" || (!blocked && primaryPrompt && isPrompt)
		if !isPrompt && d.Kind != "new_run" {
			blocked = true
		}
		if include {
			tasks = append(tasks, d.MessageID)
			if _, err = tx.ExecContext(ctx, `UPDATE message_deliveries SET prepared_attempt=? WHERE id=?`, r.AttemptID, d.ID); err != nil {
				return nil, nil, err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, messageCols+` WHERE session_id=? ORDER BY id`, r.SessionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	msgs := []Message{}
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			return nil, nil, e
		}
		msgs = append(msgs, m)
	}
	return msgs, tasks, rows.Err()
}
func (s *Store) ClaimRun(ctx context.Context, d Daemon, lease int64) (RunClaim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunClaim{}, err
	}
	defer tx.Rollback()
	if err = s.failUnavailableRuns(ctx, tx); err != nil {
		return RunClaim{}, err
	}
	r, err := scanRun(tx.QueryRowContext(ctx, runCols+` WHERE status='pending' AND daemon_id=? ORDER BY created_at,message_id,id LIMIT 1`, d.ID))
	if errors.Is(err, ErrNotFound) {
		if e := tx.Commit(); e != nil {
			return RunClaim{}, e
		}
		return RunClaim{}, ErrNotFound
	}
	if err != nil {
		return RunClaim{}, err
	}
	r.Status = "running"
	r.AttemptID = randomID("ra_")
	until := s.unix() + lease
	r.LeaseUntil = &until
	if _, err = tx.ExecContext(ctx, `UPDATE session_runs SET status='running',attempt_id=?,lease_until=?,updated_at=? WHERE id=?`, r.AttemptID, until, s.unix(), r.ID); err != nil {
		return RunClaim{}, err
	}
	msgs, tasks, err := s.runInputs(ctx, tx, r)
	if err != nil {
		return RunClaim{}, err
	}
	if len(msgs) > 0 {
		r.ContextSeq = msgs[len(msgs)-1].ID
	}
	if _, err = tx.ExecContext(ctx, `UPDATE session_runs SET context_seq=? WHERE id=?`, r.ContextSeq, r.ID); err != nil {
		return RunClaim{}, err
	}
	var input string
	if err = tx.QueryRowContext(ctx, `SELECT content FROM messages WHERE id=?`, r.MessageID).Scan(&input); err != nil {
		return RunClaim{}, err
	}
	cl := RunClaim{Run: r, Input: json.RawMessage(input), Messages: msgs, TaskMessageIDs: tasks}
	ss, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, r.SessionID)
	if err != nil {
		return cl, err
	}
	if ss.WorkID != "" {
		var agent sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT instructions,coordinator_agent_id FROM works WHERE id=?`, ss.WorkID).Scan(&cl.Instructions, &agent); err != nil {
			return cl, err
		}
		if ss.WorkRole == WorkCoordinator && agent.String == r.AgentID {
			cl.WorkToken = RandomToken("wt_")
			if _, err = tx.ExecContext(ctx, `INSERT INTO work_run_tokens VALUES(?,?,?,?)`, HashSecret(cl.WorkToken), ss.WorkID, r.ID, r.AttemptID); err != nil {
				return cl, err
			}
		}
	}
	if err = multiStatus(ctx, tx, r.SessionID, s.unix()); err != nil {
		return cl, err
	}
	return cl, tx.Commit()
}
func (s *Store) failUnavailableRuns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, runCols+` WHERE status='pending' ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	var pending []Run
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range pending {
		var reason error
		if r.AgentID == "" {
			reason = fmt.Errorf("agent 已删除")
		} else {
			reason = runRuntime(ctx, tx, Agent{DaemonID: r.DaemonID, Harness: r.Harness})
		}
		if reason == nil {
			var n int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE session_id=? AND NOT EXISTS(SELECT 1 FROM daemon_capabilities WHERE daemon_id=? AND capability='attachments-v1')`, r.SessionID, r.DaemonID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				reason = ErrAttachmentCapability
			}
		}
		if reason != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "error": reason.Error()})
			if err = s.finishRun(ctx, tx, r, "failed", b, false); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) finishRun(ctx context.Context, tx *sql.Tx, r Run, status string, payload json.RawMessage, fallback bool) error {
	now := s.unix()
	var resultID any
	if status != "cancelled" {
		m, err := insertMessage(ctx, tx, r.SessionID, RoleAssistantMsg, payload, now)
		if err != nil {
			return err
		}
		resultID = m.ID
		if _, err = tx.ExecContext(ctx, `INSERT INTO message_authors VALUES(?,?,?,?)`, m.ID, r.ID, r.AgentKey, r.AgentName); err != nil {
			return err
		}
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(payload, &body)
	if _, err := tx.ExecContext(ctx, `UPDATE session_runs SET status=?,lease_until=NULL,result_message_id=?,error=?,updated_at=? WHERE id=?`, status, resultID, body.Error, now, r.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM work_run_tokens WHERE run_id=?`, r.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE run_questions SET status='expired',updated_at=? WHERE run_id=? AND status='pending'`, now, r.ID); err != nil {
		return err
	}
	// Main tasks and confirmed steers finish with this run. Unreceived steers move to a follow-up.
	if _, err := tx.ExecContext(ctx, `UPDATE message_deliveries SET status=?,error=? WHERE run_id=? AND (kind='new_run' OR status='delivered' OR (prepared_attempt=? AND ?='succeeded'))`, status, body.Error, r.ID, r.AttemptID, status); err != nil {
		return err
	}
	pending, err := deliveries(ctx, tx, `run_id=? AND status='pending'`, r.ID)
	if err != nil {
		return err
	}
	if !fallback || status == "cancelled" {
		if _, err = tx.ExecContext(ctx, `UPDATE message_deliveries SET status=?,error=? WHERE run_id=? AND status='pending'`, status, body.Error, r.ID); err != nil {
			return err
		}
	} else if len(pending) > 0 {
		ss, e := s.getSession(ctx, tx, User{Role: RoleAdmin}, r.SessionID)
		if e != nil {
			return e
		}
		a, e := s.runAgent(ctx, tx, User{ID: ss.UserID}, ss, r.AgentID)
		var next Run
		if e == nil {
			next, e = s.newRun(ctx, tx, ss, a, pending[0].MessageID)
		}
		if e != nil {
			if _, err = tx.ExecContext(ctx, `UPDATE message_deliveries SET status='failed',error=? WHERE run_id=? AND status='pending'`, e.Error(), r.ID); err != nil {
				return err
			}
		} else {
			if _, err = tx.ExecContext(ctx, `UPDATE message_deliveries SET run_id=?,kind='steer',prepared_attempt='' WHERE run_id=? AND status='pending'`, next.ID, r.ID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE message_deliveries SET kind='new_run' WHERE id=?`, pending[0].ID); err != nil {
				return err
			}
		}
	}
	if err = multiStatus(ctx, tx, r.SessionID, now); err != nil {
		return err
	}
	return s.notifyRunMessages(ctx, tx, r.SessionID)
}
func (s *Store) RunSteers(ctx context.Context, run string) ([]Delivery, error) {
	return deliveries(ctx, s.db, `run_id=? AND kind='steer' AND status='pending' AND prepared_attempt != (SELECT attempt_id FROM session_runs WHERE id=message_deliveries.run_id) AND message_id IN (SELECT message_id FROM message_formats WHERE is_prompt=1)`, run)
}
func (s *Store) RunTaskPrompt(ctx context.Context, messageID int64) (string, error) {
	var b string
	err := s.db.QueryRowContext(ctx, `SELECT content FROM messages WHERE id=? AND EXISTS(SELECT 1 FROM message_formats f WHERE f.message_id=messages.id AND f.is_prompt=1)`, messageID).Scan(&b)
	var p struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal([]byte(b), &p)
	return p.Prompt, err
}
func (s *Store) CancelRun(ctx context.Context, u User, session, id string) (Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	if _, err = s.getSession(ctx, tx, u, session); err != nil {
		return Run{}, err
	}
	r, err := scanRun(tx.QueryRowContext(ctx, runCols+` WHERE id=? AND session_id=?`, id, session))
	if err != nil {
		return r, err
	}
	if r.Status == "cancelled" {
		return r, tx.Commit()
	}
	if r.Status != "pending" && r.Status != "running" && r.Status != "waiting" {
		return r, ErrSessionState
	}
	b, _ := json.Marshal(map[string]string{"by": u.ID, "reason": "用户取消"})
	if _, err = tx.ExecContext(ctx, `INSERT INTO run_events(session_id,run_id,attempt_id,message_id,daemon_id,event_seq,kind,payload,created_at) VALUES(?,?,?,?,?,(SELECT COALESCE(MAX(event_seq),0)+1 FROM run_events WHERE run_id=? AND attempt_id=?),'cancelled',?,?)`, r.SessionID, r.ID, r.AttemptID, r.MessageID, r.DaemonID, r.ID, r.AttemptID, string(b), s.unix()); err != nil {
		return r, err
	}
	if err = s.finishRun(ctx, tx, r, "cancelled", json.RawMessage(`{"error":"已取消"}`), false); err != nil {
		return r, err
	}
	r.Status = "cancelled"
	r.LeaseUntil = nil
	return r, tx.Commit()
}
func (s *Store) RequeueRuns(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `UPDATE session_runs SET status='pending',lease_until=NULL,updated_at=? WHERE status IN ('running','waiting') AND lease_until<=? RETURNING id,session_id`, s.unix(), s.unix())
	if err != nil {
		return err
	}
	var ids, sessions []string
	for rows.Next() {
		var id, ss string
		if err = rows.Scan(&id, &ss); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		sessions = append(sessions, ss)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i, id := range ids {
		r, e := scanRun(tx.QueryRowContext(ctx, runCols+` WHERE id=?`, id))
		if e != nil {
			return e
		}
		if e = s.recoverRunAnswers(ctx, tx, r, `1=1`); e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE message_deliveries SET status='pending',prepared_attempt='' WHERE run_id=? AND status='delivered'`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE run_questions SET status='expired',updated_at=? WHERE run_id=? AND status='pending'`, s.unix(), id); err != nil {
			return err
		}
		if err = multiStatus(ctx, tx, sessions[i], s.unix()); err != nil {
			return err
		}
	}
	if err = s.failUnavailableRuns(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReconcileRuns(ctx context.Context, daemon string, live map[string]string) ([]Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, runCols+` WHERE daemon_id=? AND status IN ('running','waiting')`, daemon)
	if err != nil {
		return nil, err
	}
	var runs []Run
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		runs = append(runs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	valid := []Run{}
	for _, r := range runs {
		if live[r.ID] == r.AttemptID && r.LeaseUntil != nil && *r.LeaseUntil > s.unix() {
			valid = append(valid, r)
			continue
		}
		if err = s.recoverRunAnswers(ctx, tx, r, `1=1`); err != nil {
			return nil, err
		}
		for _, q := range []string{`UPDATE session_runs SET status='pending',lease_until=NULL WHERE id=?`, `UPDATE message_deliveries SET status='pending',prepared_attempt='' WHERE run_id=? AND status='delivered'`, `UPDATE run_questions SET status='expired' WHERE run_id=? AND status='pending'`, `DELETE FROM work_run_tokens WHERE run_id=?`} {
			if _, err = tx.ExecContext(ctx, q, r.ID); err != nil {
				return nil, err
			}
		}
		if err = multiStatus(ctx, tx, r.SessionID, s.unix()); err != nil {
			return nil, err
		}
	}
	return valid, tx.Commit()
}

func (s *Store) PendingRunSteers(ctx context.Context) ([]Delivery, error) {
	return deliveries(ctx, s.db, `kind='steer' AND status='pending' AND EXISTS(SELECT 1 FROM session_runs r WHERE r.id=message_deliveries.run_id AND r.status='running' AND r.attempt_id!=message_deliveries.prepared_attempt) AND message_id IN (SELECT message_id FROM message_formats WHERE is_prompt=1)`)
}

// Credential removal must revoke run-scoped work tokens in the same transaction.
func (s *Store) terminateDaemonRuns(ctx context.Context, tx *sql.Tx, daemon, reason string) error {
	for {
		rows, err := tx.QueryContext(ctx, runCols+` WHERE daemon_id=? AND status IN ('pending','running','waiting')`, daemon)
		if err != nil {
			return err
		}
		var runs []Run
		for rows.Next() {
			r, e := scanRun(rows)
			if e != nil {
				rows.Close()
				return e
			}
			runs = append(runs, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return nil
		}
		for _, r := range runs {
			if err = s.recoverRunAnswers(ctx, tx, r, `1=1`); err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{"ok": false, "error": reason})
			if err = s.finishRun(ctx, tx, r, "failed", payload, false); err != nil {
				return err
			}
		}
	}
}
