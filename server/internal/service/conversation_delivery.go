package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
)

// prepareMessage assembles conversation context for a fixed delivery assignment.
func (s *ConversationService) prepareMessage(ctx context.Context, tx *sql.Tx, p Delivery) (protocol.ConversationMessage, error) {
	ss, err := s.getConversation(ctx, tx, User{Role: RoleAdmin}, p.ConversationID)
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	out := protocol.ConversationMessage{ConversationID: p.ConversationID, MessageID: p.MessageID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, Content: p.Content, Attachments: []attachment.Attachment{}}
	instructions := ""
	if ss.WorkID != "" {
		instructions, err = s.works.conversationInstructions(ctx, tx, ss, p.AgentID)
		if err != nil {
			return out, err
		}
	}
	source, err := store.ScanMessage(tx.QueryRowContext(ctx, messageCols+` WHERE id=? AND conversation_id=?`, p.MessageID, p.ConversationID))
	if err != nil {
		return out, err
	}
	for _, file := range source.Attachments {
		out.Attachments = append(out.Attachments, file)
	}
	if strings.TrimSpace(p.Content) == "" && len(source.Attachments) > 0 {
		out.Content = "Please review the attachments in this message."
	}

	if len(out.Attachments) > 0 {
		instructions += "\n# Current message attachments\nFiles are fetched on demand. Use context attachment download PATH with a conversation-relative path below to save a file into the workspace before reading it.\n"
		for _, file := range out.Attachments {
			instructions += fmt.Sprintf("- %s\n", file.RelativePath)
		}
	}
	// History is assembled by Server after Client selects its actual local session.
	out.Context = instructions
	return out, nil
}

// deliveryChanged runs after a delivery status transaction commits. Work summary
// failures are retryable and do not undo acknowledged execution or transport facts.
func (s *ConversationService) deliveryChanged(ctx context.Context, conversation string) {
	s.notifyConversation(ctx, conversation)
	if err := s.works.reconcileWorkConversation(ctx, conversation); err != nil {
		s.log.Printf("work summary pending: %v", err)
	}
	s.notifyCoordinator(ctx, conversation)
}
