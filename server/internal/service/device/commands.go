package device

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

func (s *Service) DeleteDevice(ctx context.Context, u core.User, id string) error {
	d, err := s.GetDevice(ctx, u, id)
	if err != nil {
		return err
	}
	if err = s.deleteDevice(ctx, u, id); err != nil {
		return err
	}
	s.Notifier.Disconnect(id)
	s.NotifyDeviceConversations(ctx, id)
	s.Notifier.KickUser(d.UserID)
	s.NotifyResource("devices", id, d.UserID)
	return nil
}

func (s *Service) ResetDeviceSetup(ctx context.Context, u core.User, id string) (core.Device, error) {
	d, err := s.resetDeviceSetup(ctx, u, id)
	if err == nil {
		s.Notifier.Disconnect(d.ID)
		s.NotifyDeviceConversations(ctx, d.ID)
		s.NotifyResource("devices", d.ID, d.UserID)
	}
	return d, err
}

func (s *Service) SetDeviceHarnessEnabled(ctx context.Context, u core.User, id, harness string, enabled bool) error {
	if err := s.setDeviceHarnessEnabled(ctx, u, id, harness, enabled); err != nil {
		return err
	}
	d, err := s.GetDevice(ctx, u, id)
	if err != nil {
		return err
	}
	s.NotifyResource("devices", id, d.UserID)
	if enabled {
		s.Notifier.KickUser(d.UserID)
		if failure := s.WorkService.ReconcileWorkNotifications(ctx); failure != nil {
			s.Log.Printf("work summary pending: %v", failure)
		}
	}
	return nil
}
