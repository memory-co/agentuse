package service

import (
	"context"
)

func (s *ConversationService) CreateConversation(ctx context.Context, u User, work, title string, input string, toAgentIDs, toUserIDs []string) (Conversation, *Reply, error) {
	ss, reply, err := s.createConversation(ctx, u, work, title, input, toAgentIDs, toUserIDs)
	if err == nil {
		s.Notifier.KickAll()
		s.notifyConversation(ctx, ss.ID)
	}
	return ss, reply, err
}

func (s *ConversationService) SendMessage(ctx context.Context, u User, conversation string, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (Reply, error) {
	reply, err := s.sendMessage(ctx, u, conversation, input, toAgentIDs, toUserIDs, attachmentIDs, replyToMessageID)
	if err == nil {
		s.Notifier.KickAll()
		s.notifyConversation(ctx, conversation)
	}
	return reply, err
}
