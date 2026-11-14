package conversation

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

func (s *Service) CreateConversation(ctx context.Context, u core.User, work, title string, input string, toAgentIDs, toUserIDs []string) (core.Conversation, *core.Reply, error) {
	ss, reply, err := s.createConversation(ctx, u, work, title, input, toAgentIDs, toUserIDs)
	if err == nil {
		s.Notifier.KickAll()
		s.NotifyConversation(ctx, ss.ID)
	}
	return ss, reply, err
}

func (s *Service) SendMessage(ctx context.Context, u core.User, conversation string, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (core.Reply, error) {
	reply, err := s.sendMessage(ctx, u, conversation, input, toAgentIDs, toUserIDs, attachmentIDs, replyToMessageID)
	if err == nil {
		s.Notifier.KickAll()
		s.NotifyConversation(ctx, conversation)
	}
	return reply, err
}
