package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/memory-co/agentuse/internal/protocol"
)

func (s *Service) resolveEventRoute(ctx context.Context, device string, e *protocol.ClientEvent) error {
	var raw struct {
		Type  string `json:"type"`
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(e.Event, &raw); err != nil {
		return err
	}
	var conversation, agent, version, harness, revision string
	if raw.Type == "accepted" || (raw.Type == "log" && e.MessageID != "") {
		if e.MessageID == "" || e.AgentID == "" {
			return fmt.Errorf("source message and Agent required")
		}
		err := s.db.QueryRowContext(ctx, `SELECT p.session_id,p.agent_id,p.agent_version_id,p.harness,COALESCE((SELECT revision FROM daemon_harnesses h WHERE h.daemon_id=p.daemon_id AND h.harness=p.harness),'') FROM message_agents p WHERE p.message_id=? AND p.agent_id=? AND p.daemon_id=? AND p.status IN ('sent','unknown','accepted','completed','failed','cancelled')`, e.MessageID, e.AgentID, device).Scan(&conversation, &agent, &version, &harness, &revision)
		if err != nil {
			return err
		}
	} else {
		err := s.db.QueryRowContext(ctx, `SELECT session_id,agent_id,agent_version_id,harness,harness_revision FROM runs WHERE daemon_id=? AND client_session_id=? AND id=?`, device, e.SessionID, raw.RunID).Scan(&conversation, &agent, &version, &harness, &revision)
		if err != nil && raw.Type == "log" {
			err = s.db.QueryRowContext(ctx, `SELECT session_id,agent_id,agent_version_id,'','' FROM run_events WHERE daemon_id=? AND client_session_id=? LIMIT 1`, device, e.SessionID).Scan(&conversation, &agent, &version, &harness, &revision)
		}
		if err != nil {
			return err
		}
	}
	if e.ConversationID != "" && e.ConversationID != conversation || e.AgentID != "" && e.AgentID != agent || e.AgentVersionID != "" && e.AgentVersionID != version || e.Harness != "" && harness != "" && e.Harness != harness {
		return fmt.Errorf("event route conflict")
	}
	e.ConversationID = conversation
	e.AgentID = agent
	e.AgentVersionID = version
	if e.Harness == "" {
		e.Harness = harness
	}
	if e.Revision == "" {
		e.Revision = revision
	}
	return nil
}
