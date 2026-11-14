package conversation

import (
	"context"
	"fmt"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

// ResolveConversation 把 idle 的会话标记为已完成;已经是已完成的直接返回。协调者不能标记。
func (s *Service) ResolveConversation(ctx context.Context, u core.User, id string) (core.Conversation, error) {
	return s.setResolved(ctx, u, id, core.ConversationIdle, core.ConversationResolved)
}

// ReopenConversation 取消已完成，回到 idle。
func (s *Service) ReopenConversation(ctx context.Context, u core.User, id string) (core.Conversation, error) {
	return s.setResolved(ctx, u, id, core.ConversationResolved, core.ConversationIdle)
}

func (s *Service) setResolved(ctx context.Context, u core.User, id, from, to string) (core.Conversation, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return core.Conversation{}, err
	}
	defer tx.Rollback()
	ss, err := s.LookupConversation(ctx, tx, u, id)
	if err != nil {
		return core.Conversation{}, err
	}
	if ss.Status == to {
		return ss, nil
	}
	if ss.Status != from {
		return core.Conversation{}, fmt.Errorf("%w:现在是 %s", core.ErrConversationState, ss.Status)
	}
	now := s.Unix()
	if _, err := tx.ExecContext(ctx, store.SetResolvedUpdate, to, now, ss.ID); err != nil {
		return core.Conversation{}, err
	}
	ss.Status, ss.UpdatedAt = to, now
	err = tx.Commit()
	if err == nil {
		s.PublishConversationChange(ss, "conversations")
	}
	return ss, err
}
