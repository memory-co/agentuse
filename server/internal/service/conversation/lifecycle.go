package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/service/run"
	"gorm.io/gorm"
)

func (s *Service) RenameConversation(ctx context.Context, u agentuse.User, id, title string) (agentuse.Conversation, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return agentuse.Conversation{}, fmt.Errorf("会话名称不能为空")
	}
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return agentuse.Conversation{}, err
	}
	defer tx.Rollback()
	conversation, err := s.LookupConversation(ctx, tx, u, id)
	if err != nil || conversation.Title == title {
		return conversation, err
	}
	if _, err = database.Exec(ctx, tx, database.RenameConversationUpdate, title, id); err != nil {
		return agentuse.Conversation{}, err
	}
	if err := tx.Commit().Error; err != nil {
		return agentuse.Conversation{}, err
	}
	conversation.Title = title
	s.PublishConversationChange(conversation, "conversations")
	if conversation.ParentConversationID != "" {
		s.NotifyConversation(ctx, conversation.ParentConversationID)
	}
	return conversation, nil
}

// ResolveConversation records a user's decision that a conversation is complete.
func (s *Service) ResolveConversation(ctx context.Context, u agentuse.User, id string) (agentuse.Conversation, error) {
	return s.setResolved(ctx, u, id, true, nil, 0)
}

// ResolveChildConversation accepts the creating coordinator's verdict for a reported round.
func (s *Service) ResolveChildConversation(ctx context.Context, source run.AgentSource, id string, round int64) (agentuse.Conversation, error) {
	return s.setResolved(ctx, source.User, id, true, &source, round)
}

// ReopenConversation 取消已完成，回到 idle。
func (s *Service) ReopenConversation(ctx context.Context, u agentuse.User, id string) (agentuse.Conversation, error) {
	return s.setResolved(ctx, u, id, false, nil, 0)
}

func (s *Service) setResolved(ctx context.Context, u agentuse.User, id string, resolved bool, source *run.AgentSource, round int64) (agentuse.Conversation, error) {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return agentuse.Conversation{}, err
	}
	defer tx.Rollback()
	ss, err := s.LookupConversation(ctx, tx, u, id)
	if err != nil {
		return agentuse.Conversation{}, err
	}
	if source != nil {
		if ss.ParentAgentID != source.AgentID {
			return agentuse.Conversation{}, ErrConversationScope
		}
		if err := s.checkAgentAccessIn(ctx, tx, *source, id, true); err != nil {
			return agentuse.Conversation{}, err
		}
		if err := s.requireLatestReportedRound(ctx, tx, id, round); err != nil {
			return agentuse.Conversation{}, err
		}
	}
	from, to := agentuse.ConversationIdle, agentuse.ConversationResolved
	if !resolved {
		from, to = to, from
	}
	if ss.Status == to {
		return ss, nil
	}
	if ss.Status != from {
		return agentuse.Conversation{}, fmt.Errorf("%w:现在是 %s", ErrConversationState, ss.Status)
	}
	now := s.Now().Unix()
	if _, err := database.Exec(ctx, tx, database.SetResolvedUpdate, to, now, ss.ID); err != nil {
		return agentuse.Conversation{}, err
	}
	ss.Status, ss.UpdatedAt = to, now
	err = tx.Commit().Error
	if err == nil {
		s.PublishConversationChange(ss, "conversations")
	}
	return ss, err
}

func (s *Service) requireLatestReportedRound(ctx context.Context, tx *gorm.DB, id string, seq int64) error {
	round, err := scanRound(database.Row(ctx, tx, roundColumns+` WHERE conversation_id=? ORDER BY seq DESC LIMIT 1`, id))
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return err
	}
	if round.Seq != seq || round.FinishedAt == nil {
		return fmt.Errorf("%w:只可标记子会话最新且已结束的轮次，请检查当前进度", ErrConversationState)
	}
	if err := s.projectRoundDelivery(ctx, tx, &round); err != nil {
		return err
	}
	if round.ParentDelivery != "delivered" {
		return fmt.Errorf("%w:该轮次的通知尚未送达父会话", ErrConversationState)
	}
	return nil
}
