package run

import (
	"context"
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

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
