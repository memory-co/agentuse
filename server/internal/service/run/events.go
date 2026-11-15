package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

type ExecutionFacts struct {
	Type      string             `json:"type"`
	RunID     string             `json:"run_id"`
	InputID   string             `json:"input_id"`
	InputIDs  []string           `json:"input_ids"`
	RequestID string             `json:"request_id"`
	State     string             `json:"state"`
	Status    string             `json:"status"`
	Output    string             `json:"output"`
	Model     *string            `json:"model"`
	Usage     *agentuse.RunUsage `json:"usage"`
	Error     *string            `json:"error"`
}

type ExecutionEventConsumer interface {
	Applied(context.Context, *gorm.DB, protocol.ClientEvent, ExecutionFacts, *agentuse.Run) error
	Committed(context.Context, string, bool)
}

// applyClientEvent projects only execution tables and delegates conversation effects.
func (s *Service) ApplyClientEvent(ctx context.Context, device string, envelope protocol.ClientEvent, consumer ExecutionEventConsumer) error {
	tq := database.MessageTargets(s.DB, "m", "t")

	if err := s.resolveEventRoute(ctx, device, &envelope); err != nil {
		return err
	}
	if envelope.Seq <= 0 || envelope.SessionID == "" || len(envelope.SessionID) > 128 || envelope.AgentID == "" || envelope.AgentVersionID == "" || !json.Valid(envelope.Event) {
		return fmt.Errorf("invalid client event")
	}
	var event ExecutionFacts
	if err := json.Unmarshal(envelope.Event, &event); err != nil {
		return err
	}
	switch event.Type {
	case "accepted", "message", "request", "result", "log":
	default:
		return fmt.Errorf("unknown public event type")
	}
	if event.Type != "log" {
		if id, err := uuid.Parse(event.RunID); err != nil || id.String() != event.RunID {
			return fmt.Errorf("invalid client run UUID")
		}
	}
	err := s.Database.Tx(ctx, func(tx *gorm.DB) error {
		var completed *agentuse.Run
		var existing, existingConversation, existingAgent, existingVersion, existingMessage string
		err := database.Row(ctx, tx, `SELECT payload,conversation_id,agent_id,agent_version_id,message_id FROM run_events WHERE device_id=? AND session_id=? AND event_seq=?`, device, envelope.SessionID, envelope.Seq).Scan(&existing, &existingConversation, &existingAgent, &existingVersion, &existingMessage)
		if err == nil {
			if existing != string(envelope.Event) || existingConversation != envelope.ConversationID || existingAgent != envelope.AgentID || existingVersion != envelope.AgentVersionID || existingMessage != envelope.MessageID {
				return fmt.Errorf("event sequence conflict")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var authorized int
		if err = database.Row(ctx, tx, `SELECT count(*) FROM `+tq.From+` WHERE `+tq.Device+`=? AND m.conversation_id=? AND `+tq.Key+`=? AND `+tq.Version+`=? AND `+tq.Dispatched, device, envelope.ConversationID, envelope.AgentID, envelope.AgentVersionID).Scan(&authorized); err != nil {
			return err
		}
		if authorized == 0 {
			return database.ErrNotFound
		}
		var rebound int
		if err = database.Row(ctx, tx, `SELECT count(*) FROM run_events WHERE device_id=? AND session_id=? AND (conversation_id<>? OR agent_id<>? OR agent_version_id<>?)`, device, envelope.SessionID, envelope.ConversationID, envelope.AgentID, envelope.AgentVersionID).Scan(&rebound); err != nil {
			return err
		}
		if rebound > 0 {
			return fmt.Errorf("client session route conflict")
		}
		if event.Type == "log" && event.RunID != "" {
			var owner, clientSessionID, agentID string
			err := database.Row(ctx, tx, `SELECT device_id,session_id,agent_id FROM runs WHERE id=?`, event.RunID).Scan(&owner, &clientSessionID, &agentID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil && (owner != device || clientSessionID != envelope.SessionID || agentID != envelope.AgentID) {
				return database.ErrNotFound
			}
		}
		now := s.Now().Unix()
		if event.Type == "accepted" {
			if envelope.MessageID == "" || event.InputID == "" || len(event.InputID) > 128 {
				return fmt.Errorf("accepted requires message and input identity")
			}
			prompt, err := s.DispatchedTarget(ctx, tx, device, envelope.ConversationID, envelope.MessageID, envelope.AgentID)
			if err != nil {
				return err
			}
			if prompt.AgentVersionID != envelope.AgentVersionID {
				return fmt.Errorf("accepted Agent version conflict")
			}
			var previousRun, previousInput string
			err = database.Row(ctx, tx, `SELECT i.run_id,i.input_id FROM run_inputs i JOIN runs r ON r.id=i.run_id WHERE i.message_id=? AND r.agent_id=?`, prompt.MessageID, prompt.AgentID).Scan(&previousRun, &previousInput)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if previousRun != "" && (previousRun != event.RunID || previousInput != event.InputID) {
				return fmt.Errorf("message already bound to another accepted input")
			}
			if previousRun == "" && prompt.Error != nil && !prompt.Error.Uncertain {
				return fmt.Errorf("message was rejected before acceptance")
			}
			var reused int
			if err = database.Row(ctx, tx, `SELECT count(*) FROM run_inputs i JOIN runs r ON r.id=i.run_id WHERE r.device_id=? AND r.session_id=? AND i.input_id=? AND (i.run_id<>? OR i.message_id<>?)`, device, envelope.SessionID, event.InputID, event.RunID, prompt.MessageID).Scan(&reused); err != nil {
				return err
			}
			if reused > 0 {
				return fmt.Errorf("input identity belongs to another message")
			}
			if err = tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&database.Run{ID: event.RunID, ConversationID: envelope.ConversationID, FirstMessageID: prompt.MessageID, AgentID: envelope.AgentID, AgentVersionID: envelope.AgentVersionID, DeviceID: device, SessionID: envelope.SessionID, Harness: envelope.Harness, HarnessRevision: database.Text(envelope.Revision), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			r, err := database.ScanRun(database.Row(ctx, tx, nativeRunCols+` WHERE id=?`, event.RunID))
			if err != nil {
				return err
			}
			if r.DeviceID != device || r.SessionID != envelope.SessionID || r.ConversationID != envelope.ConversationID || r.AgentID != envelope.AgentID || r.AgentVersionID != envelope.AgentVersionID || (previousRun == "" && (r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled")) {
				return fmt.Errorf("accepted run ownership or lifecycle conflict")
			}
			if err = tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "run_id"}, {Name: "input_id"}}, DoNothing: true}).Create(&database.RunInput{RunID: event.RunID, InputID: event.InputID, MessageID: prompt.MessageID, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err = s.ConversationService.SaveMessageError(ctx, tx, prompt.MessageID, prompt.AgentID, nil); err != nil {
				return err
			}
		} else if event.Type != "log" {
			r, err := database.ScanRun(database.Row(ctx, tx, nativeRunCols+` WHERE id=? AND device_id=? AND session_id=? AND conversation_id=? AND agent_id=? AND agent_version_id=?`, event.RunID, device, envelope.SessionID, envelope.ConversationID, envelope.AgentID, envelope.AgentVersionID))
			if err != nil {
				return err
			}
			if r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled" {
				return fmt.Errorf("run is already terminal")
			}
			if event.Type == "request" {
				if event.RequestID == "" || len(event.RequestID) > 128 || (event.State != "pending" && event.State != "replied" && event.State != "expired") {
					return fmt.Errorf("invalid public request")
				}
				var ownerRun, ownerDevice string
				ownerErr := database.Row(ctx, tx, `SELECT run_id,device_id FROM run_requests WHERE request_id=?`, event.RequestID).Scan(&ownerRun, &ownerDevice)
				if ownerErr != nil && ownerErr != sql.ErrNoRows {
					return ownerErr
				}
				if ownerErr == nil && (ownerRun != r.ID || ownerDevice != device) {
					return fmt.Errorf("request identity belongs to another run")
				}
				request := database.RunRequest{RequestID: event.RequestID, ConversationID: r.ConversationID, RunID: r.ID, SessionID: r.SessionID, DeviceID: device, Payload: database.Text(envelope.Event), State: event.State, CreatedAt: now, UpdatedAt: now}
				if err = tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoUpdates: clause.AssignmentColumns([]string{"payload", "state", "updated_at"})}).Create(&request).Error; err != nil {
					return err
				}
				if _, err = database.Exec(ctx, tx, `UPDATE runs SET status=CASE WHEN EXISTS(SELECT 1 FROM run_requests WHERE run_id=? AND state='pending') THEN 'waiting' ELSE 'running' END,updated_at=? WHERE id=? AND status IN ('running','waiting')`, r.ID, now, r.ID); err != nil {
					return err
				}
			}
			if event.Type == "result" {
				if event.Usage != nil {
					for _, count := range []*int64{event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.CacheReadTokens, event.Usage.CacheWriteTokens} {
						if count != nil && *count < 0 {
							return fmt.Errorf("result token usage must be non-negative integers")
						}
					}
				}
				if event.Model != nil && strings.TrimSpace(*event.Model) == "" {
					return fmt.Errorf("result model must be a non-empty string or null")
				}
				if event.Status != "completed" && event.Status != "failed" && event.Status != "cancelled" {
					return fmt.Errorf("invalid public result status")
				}
				if len(event.InputIDs) == 0 {
					return fmt.Errorf("result input_ids required")
				}
				if len(r.Result) > 0 {
					return fmt.Errorf("run already has a result")
				}
				prompts, err := s.RunInputsIn(ctx, tx, r.ConversationID)
				if err != nil {
					return err
				}
				owned := map[string]bool{}
				for _, p := range prompts {
					if p.RunID == r.ID {
						owned[p.InputID] = true
					}
				}
				if len(event.InputIDs) != len(owned) {
					return fmt.Errorf("result must cover all accepted inputs")
				}
				seen := map[string]bool{}
				for _, id := range event.InputIDs {
					if !owned[id] || seen[id] {
						return fmt.Errorf("result names an unknown or duplicate input")
					}
					seen[id] = true
				}
				errorText := ""
				if event.Error != nil {
					errorText = *event.Error
				}
				if _, err = database.Exec(ctx, tx, `UPDATE runs SET status=?,output=?,error=?,result=?,updated_at=? WHERE id=?`, event.Status, event.Output, errorText, string(envelope.Event), now, r.ID); err != nil {
					return err
				}

				completed = &r
			}
		}
		if _, err = database.Exec(ctx, tx, `INSERT INTO run_events(conversation_id,session_id,run_id,agent_id,agent_version_id,device_id,event_seq,message_id,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, envelope.ConversationID, envelope.SessionID, event.RunID, envelope.AgentID, envelope.AgentVersionID, device, envelope.Seq, envelope.MessageID, event.Type, string(envelope.Event), now); err != nil {
			return err
		}
		return consumer.Applied(ctx, tx, envelope, event, completed)
	})
	if err == nil {
		consumer.Committed(ctx, envelope.ConversationID, event.Type == "result")
	}
	return err
}
func (s *Service) RunEvents(ctx context.Context, conversation string) ([]agentuse.ConversationEvent, error) {
	return s.EventsIn(ctx, s.DB, conversation)
}
func (s *Service) EventsIn(ctx context.Context, q *gorm.DB, conversation string) ([]agentuse.ConversationEvent, error) {
	rows, err := database.Rows(ctx, q, `SELECT id,conversation_id,session_id,run_id,agent_id,agent_version_id,device_id,event_seq,kind,payload,created_at FROM run_events WHERE conversation_id=? ORDER BY id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentuse.ConversationEvent{}
	for rows.Next() {
		var e agentuse.ConversationEvent
		var raw string
		if err = rows.Scan(&e.ID, &e.ConversationID, &e.SessionID, &e.RunID, &e.AgentID, &e.AgentVersionID, &e.DeviceID, &e.EventSeq, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}
