package conversation

import (
	"context"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

// ReadConversation returns one consistent view. A terminal run must never be
// paired with messages read before its result was committed.
func (s *Service) ReadConversation(ctx context.Context, u agentuse.User, id string) (agentuse.ConversationDetail, error) {
	return s.readConversation(ctx, u, id, 0, false)
}
func (s *Service) ReadRound(ctx context.Context, u agentuse.User, id string, seq int64) (agentuse.ConversationDetail, error) {
	if seq <= 0 {
		return agentuse.ConversationDetail{}, database.ErrNotFound
	}
	return s.readConversation(ctx, u, id, seq, false)
}

func (s *Service) ReadConversationSummary(ctx context.Context, u agentuse.User, id string, roundSeq int64) (agentuse.ConversationDetail, error) {
	return s.readConversation(ctx, u, id, roundSeq, true)
}

func (s *Service) readConversation(ctx context.Context, u agentuse.User, id string, roundSeq int64, summary bool) (agentuse.ConversationDetail, error) {
	var detail agentuse.ConversationDetail
	tx, err := database.Begin(ctx, s.DB)
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
	if summary {
		if detail.Runs, err = s.RunService.RunSummariesIn(ctx, tx, id); err != nil {
			return detail, err
		}
		detail.RunInputs = []agentuse.RunInput{}
		detail.Events = []agentuse.ConversationEvent{}
		detail.Questions = []agentuse.Question{}
	} else {
		if detail.Runs, err = s.RunService.RunsIn(ctx, tx, id); err != nil {
			return detail, err
		}
		if detail.RunInputs, err = s.RunService.RunInputsIn(ctx, tx, id); err != nil {
			return detail, err
		}
		if detail.Events, err = s.RunService.EventsIn(ctx, tx, id); err != nil {
			return detail, err
		}
		if detail.Questions, err = s.RunService.QuestionsIn(ctx, tx, "conversation_id=?", id); err != nil {
			return detail, err
		}
	}
	if detail.Rounds, err = s.rounds(ctx, tx, id); err != nil {
		return detail, err
	}
	if roundSeq > 0 {
		var selected *agentuse.ConversationRound
		for i := range detail.Rounds {
			if detail.Rounds[i].Seq == roundSeq {
				selected = &detail.Rounds[i]
				break
			}
		}
		if selected == nil {
			return detail, database.ErrNotFound
		}
		included := map[string]bool{}
		messages := []agentuse.Message{}
		for _, m := range detail.Messages {
			if m.Seq >= selected.StartMessageSeq && (selected.EndMessageSeq == nil || m.Seq <= *selected.EndMessageSeq) {
				messages = append(messages, m)
				included[m.ID] = true
			}
		}
		detail.Messages = messages
		runs := []agentuse.Run{}
		runIDs := map[string]bool{}
		for _, r := range detail.Runs {
			if included[r.FirstMessageID] {
				runs = append(runs, r)
				runIDs[r.ID] = true
			}
		}
		detail.Runs = runs
		inputs := []agentuse.RunInput{}
		for _, d := range detail.RunInputs {
			if included[d.MessageID] {
				inputs = append(inputs, d)
			}
		}
		detail.RunInputs = inputs
		events := []agentuse.ConversationEvent{}
		for _, e := range detail.Events {
			if runIDs[e.RunID] {
				events = append(events, e)
			}
		}
		detail.Events = events
		questions := []agentuse.Question{}
		for _, q := range detail.Questions {
			if runIDs[q.RunID] {
				questions = append(questions, q)
			}
		}
		detail.Questions = questions
		detail.Rounds = []agentuse.ConversationRound{*selected}
	}
	detail.RunCounts = map[string]int{}
	for _, run := range detail.Runs {
		detail.RunCounts[run.Status]++
	}
	return detail, tx.Commit().Error
}

func (s *Service) ReadRun(ctx context.Context, u agentuse.User, conversation, id string, metadata bool) (agentuse.RunDetail, error) {
	var out agentuse.RunDetail
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err := s.LookupConversation(ctx, tx, u, conversation); err != nil {
		return out, err
	}
	if out.Run, err = s.RunService.RunIn(ctx, tx, conversation, id); err != nil {
		return out, err
	}
	if out.Inputs, err = s.RunService.InputsForRun(ctx, tx, conversation, id); err != nil {
		return out, err
	}
	if metadata {
		out.Run.Result = nil
		out.Events = []agentuse.ConversationEvent{}
		out.Questions, err = s.RunService.QuestionsIn(ctx, tx, "conversation_id=? AND run_id=?", conversation, id)
	} else {
		out.Events, err = s.RunService.EventsForRun(ctx, tx, conversation, id)
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit().Error
}
