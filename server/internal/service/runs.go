package service

import (
	"context"
	"database/sql"
	"encoding/json"
)

const nativeRunCols = `SELECT id,conversation_id,first_message_id,agent_id,agent_version_id,device_id,session_id,harness,harness_revision,status,result_message_id,output,error,result,created_at,updated_at FROM runs`

func scanNativeRun(row scanner) (Run, error) {
	var r Run
	var raw sql.NullString
	err := row.Scan(&r.ID, &r.ConversationID, &r.FirstMessageID, &r.AgentID, &r.AgentVersionID, &r.DeviceID, &r.SessionID, &r.Harness, &r.HarnessRevision, &r.Status, &r.ResultMessageID, &r.Output, &r.Error, &raw, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return r, ErrNotFound
	}
	if raw.Valid {
		r.Result = json.RawMessage(raw.String)
	}
	return r, err
}
func (s *RunService) GetRun(ctx context.Context, u User, conversation, id string) (Run, error) {
	if _, err := s.conversations.GetConversation(ctx, u, conversation); err != nil {
		return Run{}, err
	}
	return scanNativeRun(s.db.QueryRowContext(ctx, nativeRunCols+` WHERE conversation_id=? AND id=?`, conversation, id))
}
func (s *RunService) Runs(ctx context.Context, conversation string) ([]Run, error) {
	return runs(ctx, s.db, conversation)
}
func runs(ctx context.Context, q rowsQuerier, conversation string) ([]Run, error) {
	rows, err := q.QueryContext(ctx, nativeRunCols+` WHERE conversation_id=? ORDER BY created_at,id`, conversation)
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

const messageTargetCols = `SELECT message_id,conversation_id,agent_id,agent_version_id,device_id,harness,(SELECT content FROM messages WHERE id=message_agents.message_id),status,run_id,input_id,session_id,error FROM message_agents`

func scanMessageTarget(row scanner) (Delivery, error) {
	var d Delivery
	var content string
	err := row.Scan(&d.MessageID, &d.ConversationID, &d.AgentID, &d.AgentVersionID, &d.DeviceID, &d.Harness, &content, &d.Status, &d.RunID, &d.InputID, &d.SessionID, &d.Error)
	d.Content = content
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
func (s *RunService) Deliveries(ctx context.Context, conversation string) ([]Delivery, error) {
	return deliveries(ctx, s.db, `conversation_id=?`, conversation)
}

// Execution tables deliberately have no cascading database foreign keys.
func (s *RunService) deleteConversationExecution(ctx context.Context, tx *sql.Tx, id string) error {
	for _, query := range []string{
		`DELETE FROM run_controls WHERE conversation_id=?`, `DELETE FROM run_requests WHERE conversation_id=?`,
		`DELETE FROM run_events WHERE conversation_id=?`, `DELETE FROM runs WHERE conversation_id=?`, `DELETE FROM message_agents WHERE conversation_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return nil
}

// linkRunReply preserves one result-to-message relation inside the caller's transaction.
func (s *RunService) linkRunReply(ctx context.Context, tx *sql.Tx, run, message string) error {
	result, err := tx.ExecContext(ctx, `UPDATE runs SET result_message_id=? WHERE id=? AND result_message_id IS NULL`, message, run)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConversationState
	}
	return nil
}

// assignMessageTargets validates the execution route and fixes the Agent version
// in the conversation's message transaction. It creates no run identity.
func (s *RunService) assignMessageTargets(ctx context.Context, tx *sql.Tx, conversation Conversation, message string, agentIDs []string) error {
	for _, id := range agentIDs {
		a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		if a.UserID != conversation.UserID {
			return ErrNotFound
		}
		if err = s.devices.runtimeErr(ctx, tx, a.DeviceID, a.Harness); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO message_agents(conversation_id,message_id,agent_id,agent_version_id,device_id,harness,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, conversation.ID, message, a.ID, a.CurrentVersionID, a.DeviceID, a.Harness, s.unix(), s.unix()); err != nil {
			return err
		}
	}
	return nil
}
