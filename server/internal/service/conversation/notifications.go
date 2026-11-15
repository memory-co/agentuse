package conversation

import (
	"context"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/notification"
)

func (s *Service) PublishConversationChange(conversation agentuse.Conversation, resource string) {
	s.Changes.Publish(notification.Change{Resource: resource, ID: conversation.ID, ParentConversationID: conversation.ParentConversationID}, conversation.UserID, conversation.CreatedBy)
}
func (s *Service) NotifyConversation(ctx context.Context, id string) {
	if !s.Changes.HasSubscribers() {
		return
	}
	if conversation, err := database.ScanConversation(database.Row(ctx, s.DB, database.ConversationCols+` WHERE id=?`, id)); err == nil {
		s.PublishConversationChange(conversation, "conversations")
	}
}
