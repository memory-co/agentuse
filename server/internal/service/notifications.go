package service

import (
	"context"
	"encoding/json"
	"github.com/memory-co/agentuse/internal/protocol"
)

func (s *Service) CreateMultiSession(ctx context.Context, u User, work, title string, input json.RawMessage, targets []string, key string, promptMode ...bool) (Session, *MultiReply, error) {
	ss, reply, err := s.createMultiSession(ctx, u, work, title, input, targets, key, promptMode...)
	if err == nil {
		s.Notifier.KickAll()
	}
	return ss, reply, err
}
func (s *Service) SendMultiMessage(ctx context.Context, u User, session string, input json.RawMessage, targets []string, key string, promptMode ...bool) (MultiReply, error) {
	reply, err := s.sendMultiMessage(ctx, u, session, input, targets, key, promptMode...)
	if err == nil {
		s.Notifier.KickAll()
		s.Notifier.PushRunSteers(reply.Deliveries)
	}
	return reply, err
}
func (s *Service) CancelRun(ctx context.Context, u User, session, id string) (Run, error) {
	run, err := s.cancelRun(ctx, u, session, id)
	if err == nil {
		s.Notifier.PushRun(run.DaemonID, protocol.Message{Type: protocol.TypeCancel, RunID: run.ID, AttemptID: run.AttemptID})
		s.Notifier.KickAll()
	}
	return run, err
}
func (s *Service) AnswerRunQuestion(ctx context.Context, u User, session, runID string, id int64, answers map[string]string) (Run, Question, error) {
	run, q, err := s.answerRunQuestion(ctx, u, session, runID, id, answers)
	if err == nil {
		s.Notifier.PushRun(run.DaemonID, protocol.Message{Type: protocol.TypeAnswer, Answer: &protocol.Answer{RunID: run.ID, AttemptID: run.AttemptID, MessageID: run.MessageID, ToolUseID: q.ToolUseID, Answers: q.Answers}})
	}
	return run, q, err
}
func (s *Service) ReportRunEvent(ctx context.Context, daemon, run, attempt string, seq int64, kind string, payload json.RawMessage, lease int64) error {
	if err := s.reportRunEvent(ctx, daemon, run, attempt, seq, kind, payload, lease); err != nil {
		return err
	}
	if ds, e := s.PendingRunSteers(ctx); e == nil {
		s.Notifier.PushRunSteers(ds)
	}
	s.Notifier.KickAll()
	return nil
}
func (s *Service) DeleteDaemon(ctx context.Context, u User, id string) error {
	d, err := s.GetDaemon(ctx, u, id)
	if err != nil {
		return err
	}
	if err = s.deleteDaemon(ctx, u, id); err != nil {
		return err
	}
	s.Notifier.Disconnect(id)
	s.Notifier.KickUser(d.UserID)
	return nil
}
func (s *Service) ResetDaemonSetup(ctx context.Context, u User, id string) (Daemon, error) {
	d, err := s.resetDaemonSetup(ctx, u, id)
	if err == nil {
		s.Notifier.Disconnect(d.ID)
	}
	return d, err
}
func (s *Service) SetDaemonHarnessEnabled(ctx context.Context, u User, id, harness string, enabled bool) error {
	if err := s.setDaemonHarnessEnabled(ctx, u, id, harness, enabled); err != nil {
		return err
	}
	if enabled {
		d, err := s.GetDaemon(ctx, u, id)
		if err != nil {
			return err
		}
		s.Notifier.KickUser(d.UserID)
	}
	return nil
}
