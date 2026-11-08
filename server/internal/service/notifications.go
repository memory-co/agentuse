package service

import (
	"context"
)

func (s *Service) CreateConversation(ctx context.Context, u User, work, title string, input string, toAgentIDs, toUserIDs []string) (Conversation, *Reply, error) {
	ss, reply, err := s.createConversation(ctx, u, work, title, input, toAgentIDs, toUserIDs)
	if err == nil {
		s.Notifier.KickAll()
		s.notifyConversation(ctx, ss.ID)
	}
	return ss, reply, err
}
func (s *Service) SendMessage(ctx context.Context, u User, conversation string, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (Reply, error) {
	reply, err := s.sendMessage(ctx, u, conversation, input, toAgentIDs, toUserIDs, attachmentIDs, replyToMessageID)
	if err == nil {
		s.Notifier.KickAll()
		s.notifyConversation(ctx, conversation)
	}
	return reply, err
}
func (s *Service) CancelRun(ctx context.Context, u User, conversation, id string) (Run, error) {
	return s.cancelRun(ctx, u, conversation, id)
}
func (s *Service) DeleteDevice(ctx context.Context, u User, id string) error {
	d, err := s.GetDevice(ctx, u, id)
	if err != nil {
		return err
	}
	if err = s.deleteDevice(ctx, u, id); err != nil {
		return err
	}
	s.Notifier.Disconnect(id)
	s.notifyDeviceConversations(ctx, id)
	s.Notifier.KickUser(d.UserID)
	s.notifyResource("devices", id, d.UserID)
	return nil
}
func (s *Service) ResetDeviceSetup(ctx context.Context, u User, id string) (Device, error) {
	d, err := s.resetDeviceSetup(ctx, u, id)
	if err == nil {
		s.Notifier.Disconnect(d.ID)
		s.notifyDeviceConversations(ctx, d.ID)
		s.notifyResource("devices", d.ID, d.UserID)
	}
	return d, err
}
func (s *Service) SetDeviceHarnessEnabled(ctx context.Context, u User, id, harness string, enabled bool) error {
	if err := s.setDeviceHarnessEnabled(ctx, u, id, harness, enabled); err != nil {
		return err
	}
	d, err := s.GetDevice(ctx, u, id)
	if err != nil {
		return err
	}
	s.notifyResource("devices", id, d.UserID)
	if enabled {
		s.Notifier.KickUser(d.UserID)
		if failure := s.ReconcileWorkNotifications(ctx); failure != nil {
			s.log.Printf("work summary pending: %v", failure)
		}
	}
	return nil
}
