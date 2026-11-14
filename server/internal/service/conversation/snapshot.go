package conversation

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

// ReadConversation returns one consistent view. A completed delivery must never be
// paired with messages read before its result was committed.
func (s *Service) ReadConversation(ctx context.Context, u core.User, id string) (core.ConversationDetail, error) {
	var detail core.ConversationDetail
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return detail, err
	}
	defer tx.Rollback()
	if detail.Conversation, err = s.LookupConversation(ctx, tx, u, id); err != nil {
		return detail, err
	}
	if detail.Messages, err = s.messages(ctx, tx, id); err != nil {
		return detail, err
	}
	if detail.Runs, err = s.RunService.RunsIn(ctx, tx, id); err != nil {
		return detail, err
	}
	if detail.Deliveries, err = s.RunService.DeliveriesIn(ctx, tx, "conversation_id=?", id); err != nil {
		return detail, err
	}
	if detail.Events, err = s.RunService.EventsIn(ctx, tx, id); err != nil {
		return detail, err
	}
	if detail.Questions, err = s.RunService.QuestionsIn(ctx, tx, "conversation_id=?", id); err != nil {
		return detail, err
	}
	detail.RunCounts = map[string]int{}
	for _, run := range detail.Runs {
		detail.RunCounts[run.Status]++
	}
	return detail, tx.Commit()
}
