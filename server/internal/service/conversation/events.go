package conversation

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/service/run"
)

// Conversation orchestration consumes execution facts in the same transaction.
// The run projection only knows this consumer interface, never message storage.
func (s *Service) ReportClientEvent(ctx context.Context, device string, envelope protocol.ClientEvent) error {
	return s.RunService.ApplyClientEvent(ctx, device, envelope, conversationEvents{s})
}

type conversationEvents struct{ service *Service }

func (c conversationEvents) Applied(ctx context.Context, tx *sql.Tx, envelope protocol.ClientEvent, event run.ExecutionFacts, completed *core.Run) error {
	s := c.service
	if completed != nil && event.Status != "cancelled" {
		var author string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(author_user_id,'') FROM messages WHERE id=?`, completed.FirstMessageID).Scan(&author); err != nil {
			return err
		}
		toUsers := []string{}
		if author != "" {
			toUsers = append(toUsers, author)
		}
		recipients, _ := json.Marshal(toUsers)
		text := event.Output
		if text == "" && event.Error != nil {
			text = *event.Error
		}
		message, err := s.insertMessage(ctx, tx, completed.ConversationID, core.RoleAgentMsg, text, s.Unix())
		if err != nil {
			return err
		}
		if err = s.attachRunOutputs(ctx, tx, *completed, message.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_agent_id=?,author_agent_version_id=?,reply_to_message_id=?,to_user_ids=? WHERE id=?`, completed.AgentID, completed.AgentVersionID, completed.FirstMessageID, string(recipients), message.ID); err != nil {
			return err
		}
		if err = s.RunService.LinkRunReply(ctx, tx, completed.ID, message.ID); err != nil {
			return err
		}
	}
	return s.ProjectStatus(ctx, tx, envelope.ConversationID, s.Unix())
}

func (c conversationEvents) Committed(ctx context.Context, conversation string, result bool) {
	s := c.service
	s.NotifyConversation(ctx, conversation)
	if result {
		if err := s.WorkService.ReconcileWorkConversation(ctx, conversation); err != nil {
			s.Log.Printf("work summary pending for %s: %v", conversation, err)
		}
		s.NotifyCoordinator(ctx, conversation)
		if record, err := s.GetConversation(ctx, core.User{Role: core.RoleAdmin}, conversation); err == nil {
			s.PublishConversationChange(record, "attachments")
		}
	}
}
