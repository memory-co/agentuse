package device

import (
	"context"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
)

func (s *Service) DeleteDevice(ctx context.Context, u agentuse.User, id string) error {
	d, err := s.GetDevice(ctx, u, id)
	if err != nil {
		return err
	}
	if err = s.deleteDevice(ctx, u, id); err != nil {
		return err
	}
	s.Notifier.Disconnect(id)
	s.RunService.NotifyDeviceConversations(ctx, id)
	s.Notifier.KickUser(d.UserID)
	s.Changes.Publish(notification.Change{Resource: "devices", ID: id}, d.UserID)
	return nil
}

func (s *Service) ResetDeviceSetup(ctx context.Context, u agentuse.User, id string) (agentuse.Device, error) {
	d, err := s.resetDeviceSetup(ctx, u, id)
	if err == nil {
		s.Notifier.Disconnect(d.ID)
		s.RunService.NotifyDeviceConversations(ctx, d.ID)
		s.Changes.Publish(notification.Change{Resource: "devices", ID: d.ID}, d.UserID)
	}
	return d, err
}

func (s *Service) SetDeviceHarnessEnabled(ctx context.Context, u agentuse.User, id, harness string, enabled bool) error {
	if err := s.setDeviceHarnessEnabled(ctx, u, id, harness, enabled); err != nil {
		return err
	}
	d, err := s.GetDevice(ctx, u, id)
	if err != nil {
		return err
	}
	s.Changes.Publish(notification.Change{Resource: "devices", ID: id}, d.UserID)
	if enabled {
		s.Notifier.KickUser(d.UserID)
	}
	return nil
}
