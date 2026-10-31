package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/server/internal/store"
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

func (s *Service) CheckRun(ctx context.Context, q querier, daemon, run, attempt string) (Run, error) {
	r, err := scanRun(q.QueryRowContext(ctx, runCols+store.CheckRunClause, run))
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

func (s *Service) RunAccess(ctx context.Context, daemon, session, run, attempt string) (Run, error) {
	r, e := s.CheckRun(ctx, s.db, daemon, run, attempt)
	if e == nil && r.SessionID != session {
		return Run{}, ErrNotFound
	}
	return r, e
}

func (s *Service) RenewRun(ctx context.Context, daemon, run, attempt string, lease int64) error {
	return s.repository.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.CheckRun(ctx, tx, daemon, run, attempt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, store.RenewRunUpdate, s.unix()+lease, run)
		return err
	})
}
func (s *Service) runInputs(ctx context.Context, tx *sql.Tx, r Run) ([]Message, []int64, error) {
	ds, err := deliveries(ctx, tx, store.RunInputsClause, r.ID)
	if err != nil {
		return nil, nil, err
	}
	tasks := []int64{}
	blocked := false
	var primaryPrompt bool
	if err = tx.QueryRowContext(ctx, store.RunInputsSelect, r.MessageID).Scan(&primaryPrompt); err != nil {
		return nil, nil, err
	}
	for _, d := range ds {
		var isPrompt bool
		if err = tx.QueryRowContext(ctx, store.RunInputsSelect2, d.MessageID).Scan(&isPrompt); err != nil {
			return nil, nil, err
		}
		include := d.Kind == "new_run" || (!blocked && primaryPrompt && isPrompt)
		if !isPrompt && d.Kind != "new_run" {
			blocked = true
		}
		if include {
			tasks = append(tasks, d.MessageID)
			if _, err = tx.ExecContext(ctx, store.RunInputsUpdate, r.AttemptID, d.ID); err != nil {
				return nil, nil, err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, messageCols+store.RunInputsClause2, r.SessionID)
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

func (s *Service) ClaimRun(ctx context.Context, d Daemon, lease int64) (RunClaim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunClaim{}, err
	}
	defer tx.Rollback()
	if err = s.failUnavailableRuns(ctx, tx); err != nil {
		return RunClaim{}, err
	}
	r, err := scanRun(tx.QueryRowContext(ctx, runCols+store.ClaimRunClause, d.ID))
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
	if _, err = tx.ExecContext(ctx, store.ClaimRunUpdate, r.AttemptID, until, s.unix(), r.ID); err != nil {
		return RunClaim{}, err
	}
	msgs, tasks, err := s.runInputs(ctx, tx, r)
	if err != nil {
		return RunClaim{}, err
	}
	if len(msgs) > 0 {
		r.ContextSeq = msgs[len(msgs)-1].ID
	}
	if _, err = tx.ExecContext(ctx, store.ClaimRunUpdate2, r.ContextSeq, r.ID); err != nil {
		return RunClaim{}, err
	}
	var input string
	if err = tx.QueryRowContext(ctx, store.ClaimRunSelect, r.MessageID).Scan(&input); err != nil {
		return RunClaim{}, err
	}
	cl := RunClaim{Run: r, Input: json.RawMessage(input), Messages: msgs, TaskMessageIDs: tasks}
	ss, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, r.SessionID)
	if err != nil {
		return cl, err
	}
	cl.WorkID = ss.WorkID
	if err = tx.QueryRowContext(ctx, store.ClaimRunSelect2, ss.CreatedBy).Scan(&cl.Sender.ID, &cl.Sender.Name); err != nil {
		return cl, err
	}
	if ss.WorkID != "" {
		var agent sql.NullString
		if err = tx.QueryRowContext(ctx, store.ClaimRunSelect3, ss.WorkID).Scan(&cl.Instructions, &agent); err != nil {
			return cl, err
		}
		if ss.WorkRole == WorkCoordinator && agent.String == r.AgentID {
			cl.WorkToken = RandomToken("wt_")
			if _, err = tx.ExecContext(ctx, store.ClaimRunInsert, HashSecret(cl.WorkToken), ss.WorkID, r.ID, r.AttemptID); err != nil {
				return cl, err
			}
		}
	}
	if err = multiStatus(ctx, tx, r.SessionID, s.unix()); err != nil {
		return cl, err
	}
	return cl, tx.Commit()
}

func (s *Service) failUnavailableRuns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, runCols+store.FailUnavailableRunsClause)
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
			if err = tx.QueryRowContext(ctx, store.FailUnavailableRunsSelect, r.SessionID, r.DaemonID).Scan(&n); err != nil {
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

func (s *Service) finishRun(ctx context.Context, tx *sql.Tx, r Run, status string, payload json.RawMessage, fallback bool) error {
	now := s.unix()
	var resultID any
	if status != "cancelled" {
		m, err := insertMessage(ctx, tx, r.SessionID, RoleAssistantMsg, payload, now)
		if err != nil {
			return err
		}
		resultID = m.ID
		if _, err = tx.ExecContext(ctx, store.FinishRunInsert, m.ID, r.ID, r.AgentKey, r.AgentName); err != nil {
			return err
		}
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(payload, &body)
	if _, err := tx.ExecContext(ctx, store.FinishRunUpdate, status, resultID, body.Error, now, r.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, store.FinishRunDelete, r.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, store.FinishRunUpdate2, now, r.ID); err != nil {
		return err
	}
	// Main tasks and confirmed steers finish with this run. Unreceived steers move to a follow-up.
	if _, err := tx.ExecContext(ctx, store.FinishRunUpdate3, status, body.Error, r.ID, r.AttemptID, status); err != nil {
		return err
	}
	pending, err := deliveries(ctx, tx, store.FinishRunClause, r.ID)
	if err != nil {
		return err
	}
	if !fallback || status == "cancelled" {
		if _, err = tx.ExecContext(ctx, store.FinishRunUpdate4, status, body.Error, r.ID); err != nil {
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
			if _, err = tx.ExecContext(ctx, store.FinishRunUpdate5, e.Error(), r.ID); err != nil {
				return err
			}
		} else {
			if _, err = tx.ExecContext(ctx, store.FinishRunUpdate6, next.ID, r.ID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, store.FinishRunUpdate7, pending[0].ID); err != nil {
				return err
			}
		}
	}
	if err = multiStatus(ctx, tx, r.SessionID, now); err != nil {
		return err
	}
	return s.notifyRunMessages(ctx, tx, r.SessionID)
}

func (s *Service) RunSteers(ctx context.Context, run string) ([]Delivery, error) {
	return deliveries(ctx, s.db, store.RunSteersClause, run)
}

func (s *Service) RunTaskPrompt(ctx context.Context, messageID int64) (string, error) {
	var b string
	err := s.db.QueryRowContext(ctx, store.RunTaskPromptSelect, messageID).Scan(&b)
	var p struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal([]byte(b), &p)
	return p.Prompt, err
}

func (s *Service) cancelRun(ctx context.Context, u User, session, id string) (Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	if _, err = s.getSession(ctx, tx, u, session); err != nil {
		return Run{}, err
	}
	r, err := scanRun(tx.QueryRowContext(ctx, runCols+store.CancelRunClause, id, session))
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
	if _, err = tx.ExecContext(ctx, store.CancelRunInsert, r.SessionID, r.ID, r.AttemptID, r.MessageID, r.DaemonID, r.ID, r.AttemptID, string(b), s.unix()); err != nil {
		return r, err
	}
	if err = s.finishRun(ctx, tx, r, "cancelled", json.RawMessage(`{"error":"已取消"}`), false); err != nil {
		return r, err
	}
	r.Status = "cancelled"
	r.LeaseUntil = nil
	return r, tx.Commit()
}

func (s *Service) RequeueRuns(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, store.RequeueRunsUpdate, s.unix(), s.unix())
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
		r, e := scanRun(tx.QueryRowContext(ctx, runCols+store.RequeueRunsClause, id))
		if e != nil {
			return e
		}
		if e = s.recoverRunAnswers(ctx, tx, r, `1=1`); e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, store.RequeueRunsUpdate2, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, store.RequeueRunsUpdate3, s.unix(), id); err != nil {
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

func (s *Service) ReconcileRuns(ctx context.Context, daemon string, live map[string]string) ([]Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, runCols+store.ReconcileRunsClause, daemon)
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
		for _, q := range []string{store.ReconcileRunsUpdate, store.ReconcileRunsUpdate2, store.ReconcileRunsUpdate3, store.ReconcileRunsDelete} {
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

func (s *Service) PendingRunSteers(ctx context.Context) ([]Delivery, error) {
	return deliveries(ctx, s.db, store.PendingRunSteersClause)
}

// Credential removal must revoke run-scoped work tokens in the same transaction.
func (s *Service) terminateDaemonRuns(ctx context.Context, tx *sql.Tx, daemon, reason string) error {
	for {
		rows, err := tx.QueryContext(ctx, runCols+store.TerminateDaemonRunsClause, daemon)
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
