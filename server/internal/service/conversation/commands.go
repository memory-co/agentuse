package conversation

import (
	"context"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	runservice "github.com/memory-co/agentuse/server/internal/service/run"
)

func (s *Service) CreateConversation(ctx context.Context, u agentuse.User, parent, title string, input string, toAgentIDs, toUserIDs []string, mode string, source *runservice.AgentSource) (agentuse.Conversation, *agentuse.Reply, error) {
	ss, reply, err := s.createConversation(ctx, u, parent, title, input, toAgentIDs, toUserIDs, mode, source)
	if err == nil {
		s.Notifier.KickAll()
		s.NotifyConversation(ctx, ss.ID)
	}
	return ss, reply, err
}

func (s *Service) SendMessage(ctx context.Context, u agentuse.User, conversation string, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (agentuse.Reply, error) {
	reply, err := s.sendMessage(ctx, u, conversation, input, toAgentIDs, toUserIDs, attachmentIDs, replyToMessageID)
	if err == nil {
		s.Notifier.KickAll()
		s.NotifyConversation(ctx, conversation)
	}
	return reply, err
}
