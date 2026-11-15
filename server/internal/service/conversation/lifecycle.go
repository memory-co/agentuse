package conversation

import (
	"context"
	"fmt"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/store"
)

// ResolveConversation 把 idle 的会话标记为已完成;已经是已完成的直接返回。协调者不能标记。
func (s *Service) ResolveConversation(ctx context.Context, u agentuse.User, id string) (agentuse.Conversation, error) {
	return s.setResolved(ctx, u, id, agentuse.ConversationIdle, agentuse.ConversationResolved)
}

// ReopenConversation 取消已完成，回到 idle。
func (s *Service) ReopenConversation(ctx context.Context, u agentuse.User, id string) (agentuse.Conversation, error) {
	return s.setResolved(ctx, u, id, agentuse.ConversationResolved, agentuse.ConversationIdle)
}

func (s *Service) setResolved(ctx context.Context, u agentuse.User, id, from, to string) (agentuse.Conversation, error) {
	tx, err := store.Begin(ctx, s.DB)
	if err != nil {
		return agentuse.Conversation{}, err
	}
	defer tx.Rollback()
	ss, err := s.LookupConversation(ctx, tx, u, id)
	if err != nil {
		return agentuse.Conversation{}, err
	}
	if ss.Status == to {
		return ss, nil
	}
	if ss.Status != from {
		return agentuse.Conversation{}, fmt.Errorf("%w:现在是 %s", ErrConversationState, ss.Status)
	}
	now := s.Now().Unix()
	if _, err := store.Exec(ctx, tx, store.SetResolvedUpdate, to, now, ss.ID); err != nil {
		return agentuse.Conversation{}, err
	}
	ss.Status, ss.UpdatedAt = to, now
	err = tx.Commit().Error
	if err == nil {
		s.PublishConversationChange(ss, "conversations")
	}
	return ss, err
}
