package conversation

import (
	"context"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) PublishConversationChange(conversation agentuse.Conversation, resource string) {
	s.Changes.Publish(notification.Change{Resource: resource, ID: conversation.ID, ParentConversationID: conversation.ParentConversationID}, conversation.UserID, conversation.CreatedBy)
}
func (s *Service) NotifyConversation(ctx context.Context, id string) {
	if !s.Changes.HasSubscribers() {
		return
	}
	if conversation, err := store.ScanConversation(store.Row(ctx, s.DB, store.ConversationCols+` WHERE id=?`, id)); err == nil {
		s.PublishConversationChange(conversation, "conversations")
	}
}
