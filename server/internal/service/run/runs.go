package run

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

const nativeRunCols = `SELECT id,conversation_id,first_message_id,agent_id,agent_version_id,device_id,session_id,harness,harness_revision,status,result_message_id,output,error,result,created_at,updated_at FROM runs`

func scanNativeRun(row core.Scanner) (core.Run, error) {
	var r core.Run
	var raw sql.NullString
	err := row.Scan(&r.ID, &r.ConversationID, &r.FirstMessageID, &r.AgentID, &r.AgentVersionID, &r.DeviceID, &r.SessionID, &r.Harness, &r.HarnessRevision, &r.Status, &r.ResultMessageID, &r.Output, &r.Error, &raw, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return r, core.ErrNotFound
	}
	if raw.Valid {
		r.Result = json.RawMessage(raw.String)
	}
	return r, err
}
func (s *Service) GetRun(ctx context.Context, u core.User, conversation, id string) (core.Run, error) {
	if _, err := s.ConversationService.GetConversation(ctx, u, conversation); err != nil {
		return core.Run{}, err
	}
	return scanNativeRun(s.DB.QueryRowContext(ctx, nativeRunCols+` WHERE conversation_id=? AND id=?`, conversation, id))
}

// ExecutionSource verifies Client identity against actual accepted execution facts.
// Its run/session references authenticate this request; conversation lineage only
// stores the stable Agent and conversation, not this execution's identity.
func (s *Service) ExecutionSource(ctx context.Context, device, conversation, agent, session, run string) (core.AgentSource, error) {
	out := core.AgentSource{}
	if uuid.Validate(run) != nil || conversation == "" || agent == "" || session == "" {
		return out, core.ErrConversationScope
	}
	r, err := scanNativeRun(s.DB.QueryRowContext(ctx, nativeRunCols+` WHERE id=? AND device_id=? AND conversation_id=? AND agent_id=? AND session_id=?`, run, device, conversation, agent, session))
	if err != nil {
		return out, err
	}
	return core.AgentSource{DeviceID: device, ConversationID: r.ConversationID, AgentID: r.AgentID, AgentVersionID: r.AgentVersionID, SessionID: r.SessionID, RunID: r.ID}, nil
}

func (s *Service) Runs(ctx context.Context, conversation string) ([]core.Run, error) {
	return s.RunsIn(ctx, s.DB, conversation)
}
func (s *Service) RunsIn(ctx context.Context, q core.RowsQuerier, conversation string) ([]core.Run, error) {
	rows, err := q.QueryContext(ctx, nativeRunCols+` WHERE conversation_id=? ORDER BY created_at,id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Run{}
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

func scanMessageTarget(row core.Scanner) (core.Delivery, error) {
	var d core.Delivery
	var content string
	err := row.Scan(&d.MessageID, &d.ConversationID, &d.AgentID, &d.AgentVersionID, &d.DeviceID, &d.Harness, &content, &d.Status, &d.RunID, &d.InputID, &d.SessionID, &d.Error)
	d.Content = content
	if err == sql.ErrNoRows {
		err = core.ErrNotFound
	}
	return d, err
}
func (s *Service) DeliveriesIn(ctx context.Context, q core.RowsQuerier, where string, args ...any) ([]core.Delivery, error) {
	rows, err := q.QueryContext(ctx, messageTargetCols+` WHERE `+where+` ORDER BY (SELECT seq FROM messages WHERE id=message_agents.message_id),agent_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Delivery{}
	for rows.Next() {
		d, e := scanMessageTarget(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Service) Deliveries(ctx context.Context, conversation string) ([]core.Delivery, error) {
	return s.DeliveriesIn(ctx, s.DB, `conversation_id=?`, conversation)
}

// Execution tables deliberately have no cascading database foreign keys.
func (s *Service) DeleteConversationExecution(ctx context.Context, tx *sql.Tx, id string) error {
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
func (s *Service) LinkRunReply(ctx context.Context, tx *sql.Tx, run, message string) error {
	result, err := tx.ExecContext(ctx, `UPDATE runs SET result_message_id=? WHERE id=? AND result_message_id IS NULL`, message, run)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return core.ErrConversationState
	}
	return nil
}

// assignMessageTargets validates the execution route and fixes the Agent version
// in the conversation's message transaction. It creates no run identity.
func (s *Service) AssignMessageTargets(ctx context.Context, tx *sql.Tx, conversation core.Conversation, message string, agentIDs []string) error {
	for _, id := range agentIDs {
		a, err := core.ScanAgent(tx.QueryRowContext(ctx, core.AgentCols+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		if a.UserID != conversation.UserID {
			return core.ErrNotFound
		}
		if err = s.DeviceService.CheckRuntime(ctx, tx, a.DeviceID, a.Harness); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO message_agents(conversation_id,message_id,agent_id,agent_version_id,device_id,harness,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, conversation.ID, message, a.ID, a.CurrentVersionID, a.DeviceID, a.Harness, s.Unix(), s.Unix()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) DeliveredTarget(ctx context.Context, q core.Querier, device, conversation, message, agent string) (core.Delivery, error) {
	return scanMessageTarget(q.QueryRowContext(ctx, messageTargetCols+` WHERE conversation_id=? AND message_id=? AND agent_id=? AND device_id=? AND status IN ('sent','unknown','accepted','completed','failed','cancelled')`, conversation, message, agent, device))
}

func (s *Service) LookupRun(ctx context.Context, q core.Querier, id string) (core.Run, error) {
	return scanNativeRun(q.QueryRowContext(ctx, nativeRunCols+` WHERE id=?`, id))
}
