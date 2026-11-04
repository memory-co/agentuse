package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
)

// ReportClientEvent stores the public event unchanged. Only envelope ownership,
// ordering and the five common types are server concerns, never native frames.
func (s *Service) ReportClientEvent(ctx context.Context, device string, envelope protocol.ClientEvent) error {
	if envelope.Seq <= 0 || envelope.SessionID == "" || envelope.AgentID == "" || envelope.AgentVersionID == "" || !json.Valid(envelope.Event) {
		return fmt.Errorf("invalid client event")
	}
	var event struct {
		Type      string   `json:"type"`
		RunID     string   `json:"run_id"`
		InputID   string   `json:"input_id"`
		InputIDs  []string `json:"input_ids"`
		RequestID string   `json:"request_id"`
		State     string   `json:"state"`
		Status    string   `json:"status"`
		Output    string   `json:"output"`
		Error     *string  `json:"error"`
	}
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
	return s.repository.Tx(ctx, func(tx *sql.Tx) error {
		var existing, existingConversation, existingAgent, existingVersion string
		err := tx.QueryRowContext(ctx, `SELECT payload,session_id,agent_id,agent_version_id FROM run_events WHERE daemon_id=? AND client_session_id=? AND event_seq=?`, device, envelope.SessionID, envelope.Seq).Scan(&existing, &existingConversation, &existingAgent, &existingVersion)
		if err == nil {
			if existing != string(envelope.Event) || existingConversation != envelope.ConversationID || existingAgent != envelope.AgentID || existingVersion != envelope.AgentVersionID {
				return fmt.Errorf("event sequence conflict")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var authorized int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run_prompts WHERE daemon_id=? AND session_id=? AND agent_id=? AND agent_version_id=? AND status IN ('sent','accepted','completed','failed','cancelled')`, device, envelope.ConversationID, envelope.AgentID, envelope.AgentVersionID).Scan(&authorized); err != nil {
			return err
		}
		if authorized == 0 {
			return ErrNotFound
		}
		var rebound int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run_events WHERE daemon_id=? AND client_session_id=? AND (session_id<>? OR agent_id<>? OR agent_version_id<>?)`, device, envelope.SessionID, envelope.ConversationID, envelope.AgentID, envelope.AgentVersionID).Scan(&rebound); err != nil {
			return err
		}
		if rebound > 0 {
			return fmt.Errorf("client session route conflict")
		}
		if event.Type == "log" && event.RunID != "" {
			var owner, sessionID, agentID string
			err := tx.QueryRowContext(ctx, `SELECT daemon_id,client_session_id,agent_id FROM runs WHERE id=?`, event.RunID).Scan(&owner, &sessionID, &agentID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil && (owner != device || sessionID != envelope.SessionID || agentID != envelope.AgentID) {
				return ErrNotFound
			}
		}
		now := s.unix()
		if event.Type == "accepted" {
			if envelope.PromptID <= 0 || event.InputID == "" {
				return fmt.Errorf("accepted requires prompt and input identity")
			}
			prompt, err := scanPrompt(tx.QueryRowContext(ctx, promptCols+` WHERE id=? AND daemon_id=? AND session_id=? AND agent_id=? AND agent_version_id=?`, envelope.PromptID, device, envelope.ConversationID, envelope.AgentID, envelope.AgentVersionID))
			if err != nil {
				return err
			}
			if prompt.AgentVersionID != envelope.AgentVersionID || (prompt.Status != "sent" && prompt.Status != "accepted") {
				return fmt.Errorf("prompt is not awaiting acceptance")
			}
			if prompt.RunID != "" && (prompt.RunID != event.RunID || prompt.InputID != event.InputID) {
				return fmt.Errorf("prompt already bound to another accepted input")
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO runs(id,session_id,first_message_id,agent_id,agent_version_id,daemon_id,client_session_id,harness,harness_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, event.RunID, envelope.ConversationID, prompt.MessageID, envelope.AgentID, envelope.AgentVersionID, device, envelope.SessionID, envelope.Harness, envelope.Revision, now, now); err != nil {
				return err
			}
			r, err := scanNativeRun(tx.QueryRowContext(ctx, nativeRunCols+` WHERE id=?`, event.RunID))
			if err != nil {
				return err
			}
			if r.DaemonID != device || r.ClientSessionID != envelope.SessionID || r.SessionID != envelope.ConversationID || r.AgentID != envelope.AgentID || r.AgentVersionID != envelope.AgentVersionID || r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled" {
				return fmt.Errorf("accepted run ownership or lifecycle conflict")
			}
			if _, err = tx.ExecContext(ctx, `UPDATE run_prompts SET status='accepted',run_id=?,input_id=?,client_session_id=?,updated_at=? WHERE id=?`, event.RunID, event.InputID, envelope.SessionID, now, prompt.ID); err != nil {
				return err
			}
		} else if event.Type != "log" {
			r, err := scanNativeRun(tx.QueryRowContext(ctx, nativeRunCols+` WHERE id=? AND daemon_id=? AND client_session_id=? AND session_id=? AND agent_id=? AND agent_version_id=?`, event.RunID, device, envelope.SessionID, envelope.ConversationID, envelope.AgentID, envelope.AgentVersionID))
			if err != nil {
				return err
			}
			if event.Type == "request" {
				if event.RequestID == "" || (event.State != "pending" && event.State != "replied" && event.State != "expired") {
					return fmt.Errorf("invalid public request")
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO run_requests(request_id,session_id,run_id,client_session_id,daemon_id,payload,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET payload=excluded.payload,state=excluded.state,updated_at=excluded.updated_at WHERE run_requests.run_id=excluded.run_id AND run_requests.daemon_id=excluded.daemon_id`, event.RequestID, r.SessionID, r.ID, r.ClientSessionID, device, string(envelope.Event), event.State, now, now); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=CASE WHEN EXISTS(SELECT 1 FROM run_requests WHERE run_id=? AND state='pending') THEN 'waiting' ELSE 'running' END,updated_at=? WHERE id=? AND status IN ('running','waiting')`, r.ID, now, r.ID); err != nil {
					return err
				}
			}
			if event.Type == "result" {
				if event.Status != "completed" && event.Status != "failed" && event.Status != "cancelled" {
					return fmt.Errorf("invalid public result status")
				}
				if len(event.InputIDs) == 0 {
					return fmt.Errorf("result input_ids required")
				}
				if len(r.Result) > 0 {
					return fmt.Errorf("run already has a result")
				}
				prompts, err := deliveries(ctx, tx, `run_id=?`, r.ID)
				if err != nil {
					return err
				}
				owned := map[string]bool{}
				for _, p := range prompts {
					owned[p.InputID] = true
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
				if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?,output=?,error=?,result=?,updated_at=? WHERE id=?`, event.Status, event.Output, errorText, string(envelope.Event), now, r.ID); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE run_prompts SET status=?,error=?,updated_at=? WHERE run_id=?`, event.Status, errorText, now, r.ID); err != nil {
					return err
				}

				// The final client result becomes one conversation reply, in this transaction.
				if event.Status != "cancelled" {
					payload, _ := json.Marshal(map[string]any{"ok": event.Status == "completed", "output": event.Output, "error": event.Error, "result": json.RawMessage(envelope.Event)})
					message, err := insertMessage(ctx, tx, r.SessionID, RoleAssistantMsg, payload, now)
					if err != nil {
						return err
					}
					if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_agent_id=?,author_agent_version_id=?,reply_to_message_id=? WHERE id=?`, r.AgentID, r.AgentVersionID, r.FirstMessageID, message.ID); err != nil {
						return err
					}
					if _, err = tx.ExecContext(ctx, `UPDATE runs SET result_message_id=? WHERE id=?`, message.ID, r.ID); err != nil {
						return err
					}
				}
				if err = s.notifyRunMessages(ctx, tx, r.SessionID); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO run_events(session_id,client_session_id,run_id,agent_id,agent_version_id,daemon_id,event_seq,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, envelope.ConversationID, envelope.SessionID, event.RunID, envelope.AgentID, envelope.AgentVersionID, device, envelope.Seq, event.Type, string(envelope.Event), now); err != nil {
			return err
		}
		return multiStatus(ctx, tx, envelope.ConversationID, now)
	})
}
func (s *Service) RunEvents(ctx context.Context, conversation string) ([]SessionEvent, error) {
	return runEvents(ctx, s.db, conversation)
}
func runEvents(ctx context.Context, q rowsQuerier, conversation string) ([]SessionEvent, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,session_id,client_session_id,run_id,agent_id,agent_version_id,daemon_id,event_seq,kind,payload,created_at FROM run_events WHERE session_id=? ORDER BY id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionEvent{}
	for rows.Next() {
		var e SessionEvent
		var raw string
		if err = rows.Scan(&e.ID, &e.SessionID, &e.ClientSessionID, &e.RunID, &e.AgentID, &e.AgentVersionID, &e.DaemonID, &e.EventSeq, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Service) RunQuestions(ctx context.Context, conversation string) ([]Question, error) {
	return queryRunQuestions(ctx, s.db, `session_id=?`, conversation)
}
func queryRunQuestions(ctx context.Context, q rowsQuerier, where string, args ...any) ([]Question, error) {
	rows, err := q.QueryContext(ctx, `SELECT request_id,session_id,run_id,payload,state,answer,answered_by,created_at,COALESCE((SELECT error FROM run_controls c WHERE c.request_id=run_requests.request_id AND c.kind='reply' ORDER BY c.created_at DESC,c.rowid DESC LIMIT 1),'') FROM run_requests WHERE `+where+` ORDER BY created_at,request_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Question{}
	for rows.Next() {
		var r Question
		var raw string
		var answer, by sql.NullString
		if err = rows.Scan(&r.RequestID, &r.SessionID, &r.RunID, &raw, &r.State, &answer, &by, &r.CreatedAt, &r.DeliveryError); err != nil {
			return nil, err
		}
		r.Payload = json.RawMessage(raw)
		if answer.Valid {
			r.Answer = json.RawMessage(answer.String)
		}
		r.AnsweredBy = by.String
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) ReplyRequest(ctx context.Context, u User, conversation, run, request string, answer json.RawMessage) error {
	r, err := s.GetRun(ctx, u, conversation, run)
	if err != nil {
		return err
	}
	if r.Status != "running" && r.Status != "waiting" {
		return ErrQuestionClosed
	}
	if !json.Valid(answer) {
		return fmt.Errorf("answer must be JSON")
	}
	var state string
	if err = s.db.QueryRowContext(ctx, `SELECT state FROM run_requests WHERE request_id=? AND run_id=?`, request, run).Scan(&state); err != nil {
		return err
	}
	if state != "pending" {
		return ErrQuestionClosed
	}
	_, err = s.queueControl(ctx, r, protocol.TypeReply, request, answer, u.ID)
	return err
}
