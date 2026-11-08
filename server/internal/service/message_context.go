package service

import (
	"context"
	"encoding/json"
	"fmt"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// CompleteMessages renders only the history missing from this actual local
// session. No new cursor/table: accepted message targets are the boundary.
func (s *Service) CompleteMessages(ctx context.Context, device, conversation, message, agent, localSession string) (agentuse.MessageContext, error) {
	out := agentuse.MessageContext{CompleteMessages: []agentuse.CompleteMessage{}}
	if localSession == "" || len(localSession) > 128 {
		return out, fmt.Errorf("client_session_id required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	p, err := scanMessageTarget(tx.QueryRowContext(ctx, messageTargetCols+` WHERE session_id=? AND message_id=? AND agent_id=? AND daemon_id=? AND status IN ('sent','unknown','accepted','completed','failed','cancelled')`, conversation, message, agent, device))
	if err != nil {
		return out, err
	}
	// A known local address cannot be borrowed from another conversation/version.
	var conflicts int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run_events WHERE daemon_id=? AND client_session_id=? AND (session_id<>? OR agent_id<>? OR agent_version_id<>?)`, device, localSession, conversation, agent, p.AgentVersionID).Scan(&conflicts)
	if err != nil {
		return out, err
	}
	if conflicts != 0 {
		return out, fmt.Errorf("client session route conflict")
	}
	var current, after int64
	if err = tx.QueryRowContext(ctx, `SELECT seq FROM messages WHERE id=? AND session_id=?`, message, conversation).Scan(&current); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(m.seq),0) FROM message_agents p JOIN messages m ON m.id=p.message_id WHERE p.session_id=? AND p.agent_id=? AND p.agent_version_id=? AND p.daemon_id=? AND p.client_session_id=? AND p.run_id<>'' AND p.input_id<>'' AND m.seq<?`, conversation, agent, p.AgentVersionID, device, localSession, current).Scan(&after)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, messageCols+` WHERE session_id=? AND seq>? AND seq<? AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.result_message_id=messages.id AND r.daemon_id=? AND r.client_session_id=?) ORDER BY seq`, conversation, after, current, device, localSession)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			return out, e
		}
		authorRole, authorID := "user", m.AuthorUserID
		if m.Role == RoleAssistantMsg {
			authorRole, authorID = "agent", m.AgentKey
		}
		var body struct {
			Prompt string          `json:"prompt"`
			Output string          `json:"output"`
			Error  json.RawMessage `json:"error"`
		}
		text := string(m.Content)
		if json.Unmarshal(m.Content, &body) == nil {
			if m.Role == RoleAssistantMsg {
				text = body.Output
				if text == "" && len(body.Error) > 0 && string(body.Error) != "null" {
					if json.Unmarshal(body.Error, &text) != nil {
						text = string(body.Error)
					}
				}
			} else if body.Prompt != "" {
				text = body.Prompt
			}
		}
		out.CompleteMessages = append(out.CompleteMessages, agentuse.CompleteMessage{ID: m.ID, Seq: m.Seq, AuthorRole: authorRole, AuthorID: authorID, Text: text, ToAgentIDs: m.ToAgentIDs, ToUserIDs: m.ToUserIDs, Attachments: m.Attachments})
	}
	return out, rows.Err()
}
