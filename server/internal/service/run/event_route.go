package run

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
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
		err := s.DB.QueryRowContext(ctx, `SELECT m.conversation_id,t.key,json_extract(t.value,'$.agent_version_id'),json_extract(t.value,'$.harness'),COALESCE((SELECT harness_revision FROM device_harnesses h WHERE h.device_id=json_extract(t.value,'$.device_id') AND h.harness=json_extract(t.value,'$.harness')),'') FROM `+store.MessageTargetsFrom+` WHERE m.id=? AND t.key=? AND json_extract(t.value,'$.device_id')=? AND `+store.MessageTargetDispatched, e.MessageID, e.AgentID, device).Scan(&conversation, &agent, &version, &harness, &revision)
		if err != nil {
			return err
		}
	} else {
		err := s.DB.QueryRowContext(ctx, `SELECT conversation_id,agent_id,agent_version_id,harness,harness_revision FROM runs WHERE device_id=? AND session_id=? AND id=?`, device, e.SessionID, raw.RunID).Scan(&conversation, &agent, &version, &harness, &revision)
		if err != nil && raw.Type == "log" {
			err = s.DB.QueryRowContext(ctx, `SELECT conversation_id,agent_id,agent_version_id,'','' FROM run_events WHERE device_id=? AND session_id=? LIMIT 1`, device, e.SessionID).Scan(&conversation, &agent, &version, &harness, &revision)
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
