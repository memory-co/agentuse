package service

import (
	"context"
	"fmt"

	"github.com/memory-co/agentuse/server/internal/store"
)

// ResolveConversation 把 idle 的会话标记为已完成;已经是已完成的直接返回。协调者不能标记。
func (s *ConversationService) ResolveConversation(ctx context.Context, u User, id string) (Conversation, error) {
	return s.setResolved(ctx, u, id, ConversationIdle, ConversationResolved)
}

// ReopenConversation 取消已完成，回到 idle。
func (s *ConversationService) ReopenConversation(ctx context.Context, u User, id string) (Conversation, error) {
	return s.setResolved(ctx, u, id, ConversationResolved, ConversationIdle)
}

func (s *ConversationService) setResolved(ctx context.Context, u User, id, from, to string) (Conversation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()
	ss, err := s.getConversation(ctx, tx, u, id)
	if err != nil {
		return Conversation{}, err
	}
	if ss.WorkRole == WorkCoordinator {
		return Conversation{}, ErrCoordinator
	}
	if ss.Status == to {
		return ss, nil
	}
	if ss.Status != from {
		return Conversation{}, fmt.Errorf("%w:现在是 %s", ErrConversationState, ss.Status)
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx, store.SetResolvedUpdate, to, now, ss.ID); err != nil {
		return Conversation{}, err
	}
	ss.Status, ss.UpdatedAt = to, now
	err = tx.Commit()
	if err == nil {
		s.publishConversationChange(ss, "conversations")
	}
	return ss, err
}
