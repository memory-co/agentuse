package service

import (
	"context"
	"encoding/json"
)

func (s *Service) CreateMultiSession(ctx context.Context, u User, work, title string, input json.RawMessage, toAgentIDs, toUserIDs []string) (Session, *MultiReply, error) {
	ss, reply, err := s.createMultiSession(ctx, u, work, title, input, toAgentIDs, toUserIDs)
	if err == nil {
		s.Notifier.KickAll()
	}
	return ss, reply, err
}
func (s *Service) SendMultiMessage(ctx context.Context, u User, session string, input json.RawMessage, toAgentIDs, toUserIDs, attachmentIDs []string) (MultiReply, error) {
	reply, err := s.sendMultiMessage(ctx, u, session, input, toAgentIDs, toUserIDs, attachmentIDs)
	if err == nil {
		s.Notifier.KickAll()
	}
	return reply, err
}
func (s *Service) CancelRun(ctx context.Context, u User, conversation, id string) (Run, error) {
	return s.cancelRun(ctx, u, conversation, id)
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
