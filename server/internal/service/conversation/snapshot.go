package conversation

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

// ReadConversation returns one consistent view. A completed delivery must never be
// paired with messages read before its result was committed.
func (s *Service) ReadConversation(ctx context.Context, u core.User, id string) (core.ConversationDetail, error) {
	return s.readConversation(ctx, u, id, 0)
}
func (s *Service) ReadRound(ctx context.Context, u core.User, id string, seq int64) (core.ConversationDetail, error) {
	if seq <= 0 {
		return core.ConversationDetail{}, core.ErrNotFound
	}
	return s.readConversation(ctx, u, id, seq)
}
func (s *Service) readConversation(ctx context.Context, u core.User, id string, roundSeq int64) (core.ConversationDetail, error) {
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
	if detail.Rounds, err = s.rounds(ctx, tx, id); err != nil {
		return detail, err
	}
	if roundSeq > 0 {
		var selected *core.ConversationRound
		for i := range detail.Rounds {
			if detail.Rounds[i].Seq == roundSeq {
				selected = &detail.Rounds[i]
				break
			}
		}
		if selected == nil {
			return detail, core.ErrNotFound
		}
		included := map[string]bool{}
		messages := []core.Message{}
		for _, m := range detail.Messages {
			if m.Seq >= selected.StartMessageSeq && (selected.EndMessageSeq == nil || m.Seq <= *selected.EndMessageSeq) {
				messages = append(messages, m)
				included[m.ID] = true
			}
		}
		detail.Messages = messages
		runs := []core.Run{}
		runIDs := map[string]bool{}
		for _, r := range detail.Runs {
			if included[r.FirstMessageID] {
				runs = append(runs, r)
				runIDs[r.ID] = true
			}
		}
		detail.Runs = runs
		deliveries := []core.Delivery{}
		for _, d := range detail.Deliveries {
			if included[d.MessageID] {
				deliveries = append(deliveries, d)
			}
		}
		detail.Deliveries = deliveries
		events := []core.ConversationEvent{}
		for _, e := range detail.Events {
			if runIDs[e.RunID] {
				events = append(events, e)
			}
		}
		detail.Events = events
		questions := []core.Question{}
		for _, q := range detail.Questions {
			if runIDs[q.RunID] {
				questions = append(questions, q)
			}
		}
		detail.Questions = questions
		detail.Rounds = []core.ConversationRound{*selected}
	}
	detail.RunCounts = map[string]int{}
	for _, run := range detail.Runs {
		detail.RunCounts[run.Status]++
	}
	return detail, tx.Commit()
}
