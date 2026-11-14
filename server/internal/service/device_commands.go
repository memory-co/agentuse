package service

import (
	"context"
)

func (s *DeviceService) DeleteDevice(ctx context.Context, u User, id string) error {
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

func (s *DeviceService) ResetDeviceSetup(ctx context.Context, u User, id string) (Device, error) {
	d, err := s.resetDeviceSetup(ctx, u, id)
	if err == nil {
		s.Notifier.Disconnect(d.ID)
		s.notifyDeviceConversations(ctx, d.ID)
		s.notifyResource("devices", d.ID, d.UserID)
	}
	return d, err
}

func (s *DeviceService) SetDeviceHarnessEnabled(ctx context.Context, u User, id, harness string, enabled bool) error {
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
		if failure := s.works.ReconcileWorkNotifications(ctx); failure != nil {
			s.log.Printf("work summary pending: %v", failure)
		}
	}
	return nil
}
