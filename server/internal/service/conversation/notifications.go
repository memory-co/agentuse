package conversation

import (
	"context"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/notification"
)

func (s *Service) PublishConversationChange(conversation agentuse.Conversation, resource string) {
	s.publishConversationChange(conversation, resource, false)
}

func (s *Service) publishConversationChange(conversation agentuse.Conversation, resource string, detailOnly bool) {
	s.Changes.Publish(notification.Change{Resource: resource, ID: conversation.ID, ParentConversationID: conversation.ParentConversationID, DetailOnly: detailOnly}, conversation.UserID, conversation.CreatedBy)
}

func (s *Service) NotifyConversation(ctx context.Context, id string) {
	s.notifyConversation(ctx, id, false)
}

func (s *Service) notifyConversation(ctx context.Context, id string, detailOnly bool) {
	if !s.Changes.HasSubscribers() {
		return
	}
	if conversation, err := database.ScanConversation(database.Row(ctx, s.DB, database.ConversationCols+` WHERE id=?`, id)); err == nil {
		s.publishConversationChange(conversation, "conversations", detailOnly)
	}
}
