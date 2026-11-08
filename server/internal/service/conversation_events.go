package service

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/memory-co/agentuse/internal/protocol"
)

// Conversation orchestration consumes execution facts in the same transaction.
// The run projection only knows this consumer interface, never message storage.
func (s *Service) ReportClientEvent(ctx context.Context, device string, envelope protocol.ClientEvent) error {
	return s.applyClientEvent(ctx, device, envelope, conversationEvents{s})
}

type conversationEvents struct{ service *Service }

func (c conversationEvents) Applied(ctx context.Context, tx *sql.Tx, envelope protocol.ClientEvent, event executionFacts, completed *Run) error {
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
		message, err := insertMessage(ctx, tx, completed.ConversationID, RoleAgentMsg, text, s.unix())
		if err != nil {
			return err
		}
		if err = s.attachRunOutputs(ctx, tx, *completed, message.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_agent_id=?,author_agent_version_id=?,reply_to_message_id=?,to_user_ids=? WHERE id=?`, completed.AgentID, completed.AgentVersionID, completed.FirstMessageID, string(recipients), message.ID); err != nil {
			return err
		}
		if err = linkRunReply(ctx, tx, completed.ID, message.ID); err != nil {
			return err
		}
	}
	return projectConversationStatus(ctx, tx, envelope.ConversationID, s.unix())
}

func (c conversationEvents) Committed(ctx context.Context, conversation string, result bool) {
	s := c.service
	s.notifyConversation(ctx, conversation)
	if result {
		if err := s.reconcileWorkConversation(ctx, conversation); err != nil {
			s.log.Printf("work summary pending for %s: %v", conversation, err)
		}
		s.notifyCoordinator(ctx, conversation)
		if record, err := s.GetConversation(ctx, User{Role: RoleAdmin}, conversation); err == nil {
			s.publishConversationChange(record, "attachments")
		}
	}
}
