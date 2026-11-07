package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
)

const nativeRunCols = `SELECT id,session_id,first_message_id,agent_id,agent_version_id,daemon_id,client_session_id,harness,harness_revision,status,result_message_id,output,error,result,created_at,updated_at FROM runs`

func scanNativeRun(row scanner) (Run, error) {
	var r Run
	var raw sql.NullString
	err := row.Scan(&r.ID, &r.SessionID, &r.FirstMessageID, &r.AgentID, &r.AgentVersionID, &r.DaemonID, &r.ClientSessionID, &r.Harness, &r.HarnessRevision, &r.Status, &r.ResultMessageID, &r.Output, &r.Error, &raw, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return r, ErrNotFound
	}
	if raw.Valid {
		r.Result = json.RawMessage(raw.String)
	}
	return r, err
}
func (s *Service) GetRun(ctx context.Context, u User, conversation, id string) (Run, error) {
	if _, err := s.GetSession(ctx, u, conversation); err != nil {
		return Run{}, err
	}
	return scanNativeRun(s.db.QueryRowContext(ctx, nativeRunCols+` WHERE session_id=? AND id=?`, conversation, id))
}
func (s *Service) Runs(ctx context.Context, conversation string) ([]Run, error) {
	return runs(ctx, s.db, conversation)
}
func runs(ctx context.Context, q rowsQuerier, conversation string) ([]Run, error) {
	rows, err := q.QueryContext(ctx, nativeRunCols+` WHERE session_id=? ORDER BY created_at,id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, e := scanNativeRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const messageTargetCols = `SELECT message_id,session_id,agent_id,agent_version_id,daemon_id,harness,(SELECT content FROM messages WHERE id=message_agents.message_id),status,run_id,input_id,client_session_id,error FROM message_agents`

func scanMessageTarget(row scanner) (Delivery, error) {
	var d Delivery
	var content string
	err := row.Scan(&d.MessageID, &d.SessionID, &d.AgentID, &d.AgentVersionID, &d.DaemonID, &d.Harness, &content, &d.Status, &d.RunID, &d.InputID, &d.ClientSessionID, &d.Error)
	d.Content = json.RawMessage(content)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	return d, err
}
func deliveries(ctx context.Context, q rowsQuerier, where string, args ...any) ([]Delivery, error) {
	rows, err := q.QueryContext(ctx, messageTargetCols+` WHERE `+where+` ORDER BY (SELECT seq FROM messages WHERE id=message_agents.message_id),agent_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		d, e := scanMessageTarget(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Service) Deliveries(ctx context.Context, conversation string) ([]Delivery, error) {
	return deliveries(ctx, s.db, `session_id=?`, conversation)
}
func multiStatus(ctx context.Context, tx *sql.Tx, conversation string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE sessions SET status=CASE WHEN EXISTS(SELECT 1 FROM runs WHERE session_id=? AND status='waiting') THEN 'waiting' WHEN EXISTS(SELECT 1 FROM runs WHERE session_id=? AND status='running') THEN 'running' WHEN EXISTS(SELECT 1 FROM message_agents WHERE session_id=? AND status IN ('pending','sent','unknown')) THEN 'pending' WHEN status='resolved' THEN 'resolved' ELSE 'idle' END,updated_at=? WHERE id=?`, conversation, conversation, conversation, now, conversation)
	return err
}
func (s *Service) insertMulti(ctx context.Context, tx *sql.Tx, u User, work, title string) (Session, error) {
	if work != "" {
		if _, err := getWork(ctx, tx, u, work); err != nil {
			return Session{}, err
		}
	}
	id := newUUID()
	now := s.unix()
	if _, err := tx.ExecContext(ctx, store.InsertMultiInsert, id, u.ID, u.ID, nullIfEmpty(work), title, now, now); err != nil {
		return Session{}, err
	}
	return s.getSession(ctx, tx, u, id)
}
func (s *Service) createMultiSession(ctx context.Context, u User, work, title string, input json.RawMessage, targets []string, _ string, _ ...bool) (Session, *MultiReply, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, nil, err
	}
	defer tx.Rollback()
	ss, err := s.insertMulti(ctx, tx, u, work, title)
	if err != nil {
		return ss, nil, err
	}
	var reply *MultiReply
	if input != nil {
		r, e := s.sendMultiTx(ctx, tx, u, ss, input, targets, "")
		if e != nil {
			return ss, nil, e
		}
		reply = &r
		ss, e = s.getSession(ctx, tx, u, ss.ID)
		if e != nil {
			return ss, nil, e
		}
	}
	return ss, reply, tx.Commit()
}
func (s *Service) sendMultiMessage(ctx context.Context, u User, conversation string, input json.RawMessage, targets []string, _ string, _ ...bool) (MultiReply, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MultiReply{}, err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, conversation)
	if err != nil {
		return MultiReply{}, err
	}
	out, err := s.sendMultiTx(ctx, tx, u, ss, input, targets, "")
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) sendMultiTx(ctx context.Context, tx *sql.Tx, u User, ss Session, input json.RawMessage, targets []string, _ string, _ ...bool) (MultiReply, error) {
	if !json.Valid(input) {
		return MultiReply{}, fmt.Errorf("invalid message")
	}
	seen := map[string]bool{}
	agents := []Agent{}
	for _, id := range targets {
		if seen[id] {
			continue
		}
		seen[id] = true
		a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=?`, id))
		if err != nil {
			return MultiReply{}, err
		}
		if a.UserID != ss.UserID {
			return MultiReply{}, ErrNotFound
		}
		if err = runtimeErr(ctx, tx, a.DaemonID, a.Harness); err != nil {
			return MultiReply{}, err
		}
		agents = append(agents, a)
	}
	m, err := insertMessage(ctx, tx, ss.ID, RoleUserMsg, input, s.unix())
	if err != nil {
		return MultiReply{}, err
	}
	targetJSON, _ := json.Marshal(targets)
	if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_user_id=?,run_agent_ids=? WHERE id=?`, u.ID, string(targetJSON), m.ID); err != nil {
		return MultiReply{}, err
	}
	for _, a := range agents {
		if _, err = tx.ExecContext(ctx, `INSERT INTO message_agents(session_id,message_id,agent_id,agent_version_id,daemon_id,harness,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, ss.ID, m.ID, a.ID, a.CurrentVersionID, a.DaemonID, a.Harness, s.unix(), s.unix()); err != nil {
			return MultiReply{}, err
		}
	}
	var text protocol.PromptInput
	json.Unmarshal(input, &text)
	title := []rune(strings.TrimSpace(text.Prompt))
	if len(title) > 50 {
		title = title[:50]
	}
	if ss.Title == "" {
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET title=? WHERE id=?`, string(title), ss.ID); err != nil {
			return MultiReply{}, err
		}
	}
	if err = multiStatus(ctx, tx, ss.ID, s.unix()); err != nil {
		return MultiReply{}, err
	}
	out := MultiReply{Message: &m, Runs: []Run{}}
	out.Deliveries, err = deliveries(ctx, tx, `message_id=?`, m.ID)
	return out, err
}

// Execution tables deliberately have no cascading database foreign keys.
func deleteConversationExecution(ctx context.Context, tx *sql.Tx, id string) error {
	for _, query := range []string{
		`DELETE FROM work_run_tokens WHERE message_id IN (SELECT message_id FROM message_agents WHERE session_id=?)`,
		`DELETE FROM run_notifications WHERE message_id IN (SELECT id FROM messages WHERE session_id=?)`,
		`DELETE FROM run_controls WHERE session_id=?`, `DELETE FROM run_requests WHERE session_id=?`,
		`DELETE FROM run_events WHERE session_id=?`, `DELETE FROM runs WHERE session_id=?`, `DELETE FROM message_agents WHERE session_id=?`,
		`DELETE FROM attachments WHERE session_id=?`, `DELETE FROM messages WHERE session_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return nil
}
