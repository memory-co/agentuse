package conversation

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

func (s *Service) CreateConversation(ctx context.Context, u core.User, parent, title string, input string, toAgentIDs, toUserIDs []string, mode string, source *core.AgentSource) (core.Conversation, *core.Reply, error) {
	ss, reply, err := s.createConversation(ctx, u, parent, title, input, toAgentIDs, toUserIDs, mode, source)
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
