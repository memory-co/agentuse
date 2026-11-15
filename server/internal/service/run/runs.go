package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

const nativeRunCols = `SELECT id,conversation_id,first_message_id,agent_id,agent_version_id,device_id,session_id,harness,harness_revision,status,result_message_id,output,error,result,created_at,updated_at FROM runs`

func (s *Service) GetRun(ctx context.Context, u agentuse.User, conversation, id string) (agentuse.Run, error) {
	if _, err := s.ConversationService.GetConversation(ctx, u, conversation); err != nil {
		return agentuse.Run{}, err
	}
	return database.ScanRun(database.Row(ctx, s.DB, nativeRunCols+` WHERE conversation_id=? AND id=?`, conversation, id))
}

// ExecutionSource verifies Client identity against actual accepted execution facts.
// Its run/session references authenticate this request; conversation lineage only
// stores the stable Agent and conversation, not this execution's identity.
func (s *Service) ExecutionSource(ctx context.Context, device, conversation, agent, session, run string) (AgentSource, error) {
	out := AgentSource{}
	if uuid.Validate(run) != nil || conversation == "" || agent == "" || session == "" {
		return out, ErrExecutionSource
	}
	r, err := database.ScanRun(database.Row(ctx, s.DB, nativeRunCols+` WHERE id=? AND device_id=? AND conversation_id=? AND agent_id=? AND session_id=?`, run, device, conversation, agent, session))
	if err != nil {
		return out, err
	}
	return AgentSource{DeviceID: device, ConversationID: r.ConversationID, AgentID: r.AgentID, AgentVersionID: r.AgentVersionID, SessionID: r.SessionID, RunID: r.ID}, nil
}

func (s *Service) Runs(ctx context.Context, conversation string) ([]agentuse.Run, error) {
	return s.RunsIn(ctx, s.DB, conversation)
}
func (s *Service) RunsIn(ctx context.Context, q *gorm.DB, conversation string) ([]agentuse.Run, error) {
	rows, err := database.Rows(ctx, q, nativeRunCols+` WHERE conversation_id=? ORDER BY created_at,id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentuse.Run{}
	for rows.Next() {
		r, e := database.ScanRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MessageTarget contains transport metadata embedded in an existing message.
// It has no execution state or identity; only accepted events create RunInputs.
type MessageTarget struct {
	MessageID      string                        `json:"-"`
	ConversationID string                        `json:"-"`
	AgentID        string                        `json:"-"`
	Content        string                        `json:"-"`
	AgentVersionID string                        `json:"agent_version_id"`
	DeviceID       string                        `json:"device_id"`
	Harness        string                        `json:"harness"`
	Dispatch       *protocol.ConversationMessage `json:"dispatch,omitempty"`
	Error          *agentuse.MessageError        `json:"error,omitempty"`
}

func scanMessageTarget(row database.Scanner) (MessageTarget, error) {
	var p MessageTarget
	var dispatch, receipt string
	err := row.Scan(&p.MessageID, &p.ConversationID, &p.AgentID, &p.AgentVersionID, &p.DeviceID, &p.Harness, &p.Content, &dispatch, &receipt)
	if err == sql.ErrNoRows {
		return p, database.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(dispatch), &p.Dispatch); err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(receipt), &p.Error)
	return p, err
}

func (s *Service) DispatchedTarget(ctx context.Context, q *gorm.DB, device, conversation, message, agent string) (MessageTarget, error) {
	tq := database.MessageTargets(s.DB, "m", "t")

	return scanMessageTarget(database.Row(ctx, q, tq.Columns()+` WHERE m.conversation_id=? AND m.id=? AND `+tq.Key+`=? AND `+tq.Device+`=? AND `+tq.Dispatched, conversation, message, agent, device))
}

func (s *Service) RunInputsIn(ctx context.Context, q *gorm.DB, conversation string) ([]agentuse.RunInput, error) {
	rows, err := database.Rows(ctx, q, `SELECT i.run_id,i.input_id,i.message_id,i.created_at FROM run_inputs i JOIN runs r ON r.id=i.run_id JOIN messages m ON m.id=i.message_id WHERE r.conversation_id=? ORDER BY m.seq,r.agent_id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentuse.RunInput{}
	for rows.Next() {
		var input agentuse.RunInput
		if err = rows.Scan(&input.RunID, &input.InputID, &input.MessageID, &input.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, input)
	}
	return out, rows.Err()
}

func (s *Service) RunInputs(ctx context.Context, conversation string) ([]agentuse.RunInput, error) {
	return s.RunInputsIn(ctx, s.DB, conversation)
}

// Execution tables deliberately have no cascading database foreign keys.
func (s *Service) DeleteConversationExecution(ctx context.Context, tx *gorm.DB, id string) error {
	for _, query := range []string{
		`DELETE FROM run_controls WHERE conversation_id=?`, `DELETE FROM run_requests WHERE conversation_id=?`,
		`DELETE FROM run_events WHERE conversation_id=?`, `DELETE FROM run_inputs WHERE run_id IN (SELECT id FROM runs WHERE conversation_id=?)`, `DELETE FROM runs WHERE conversation_id=?`,
	} {
		if _, err := database.Exec(ctx, tx, query, id); err != nil {
			return err
		}
	}
	return nil
}

// linkRunReply preserves one result-to-message relation inside the caller's transaction.
func (s *Service) LinkRunReply(ctx context.Context, tx *gorm.DB, run, message string) error {
	result, err := database.Exec(ctx, tx, `UPDATE runs SET result_message_id=? WHERE id=? AND result_message_id IS NULL`, message, run)
	if err != nil {
		return err
	}
	if result != 1 {
		return ErrRunState
	}
	return nil
}

func (s *Service) LookupRun(ctx context.Context, q *gorm.DB, id string) (agentuse.Run, error) {
	return database.ScanRun(database.Row(ctx, q, nativeRunCols+` WHERE id=?`, id))
}

// AgentSource is verified against an actual Client execution, never caller-supplied lineage.
type AgentSource struct {
	User           agentuse.User
	DeviceID       string
	ConversationID string
	AgentID        string
	AgentVersionID string
	SessionID      string
	RunID          string
}

var ErrExecutionSource = errors.New("execution source is outside the verified device scope")
var ErrRunState = errors.New("会话状态不对")
