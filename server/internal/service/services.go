// Package service assembles the Server's business services.
package service

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/notification"
	"github.com/memory-co/agentuse/server/internal/service/agent"
	"github.com/memory-co/agentuse/server/internal/service/conversation"
	"github.com/memory-co/agentuse/server/internal/service/device"
	"github.com/memory-co/agentuse/server/internal/service/file"
	"github.com/memory-co/agentuse/server/internal/service/run"
	"github.com/memory-co/agentuse/server/internal/service/skill"
	"github.com/memory-co/agentuse/server/internal/service/user"
)

type Service struct {
	Database            *database.DB
	DB                  *gorm.DB
	Now                 func() time.Time
	Log                 *log.Logger
	BrowserChanges      *notification.Broker
	notifier            notification.Client
	UserService         *user.Service
	DeviceService       *device.Service
	AgentService        *agent.Service
	ConversationService *conversation.Service
	RunService          *run.Service
	Files               *file.Service
	Skills              *skill.Service
}

func New(db *database.DB) *Service {
	s := &Service{Database: db, DB: db.Connection(), Now: time.Now, Log: log.Default(), BrowserChanges: &notification.Broker{}}
	now := func() time.Time { return s.Now() }
	s.UserService = &user.Service{DB: s.DB, Now: now, Changes: s.BrowserChanges}
	s.DeviceService = &device.Service{DB: s.DB, Now: now, Changes: s.BrowserChanges}
	s.AgentService = &agent.Service{DB: s.DB, Now: now, Changes: s.BrowserChanges}
	s.ConversationService = &conversation.Service{DB: s.DB, Database: db, Now: now, Log: s.Log, Changes: s.BrowserChanges}
	s.RunService = &run.Service{DB: s.DB, Database: db, Now: now}
	s.DeviceService.ConversationService = s.ConversationService
	s.AgentService.DeviceService, s.AgentService.ConversationService = s.DeviceService, s.ConversationService
	s.ConversationService.RunService = s.RunService
	s.ConversationService.DeviceService = s.DeviceService
	s.RunService.DeviceService, s.RunService.ConversationService, s.RunService.AgentService = s.DeviceService, s.ConversationService, s.AgentService
	s.Skills = &skill.Service{DB: s.DB, Now: now, OnChange: func(id, owner string) {
		s.BrowserChanges.Publish(notification.Change{Resource: "skills", ID: id}, owner)
	}}
	s.AgentService.Skills, s.RunService.Skills = s.Skills, s.Skills
	s.SetNotifier(notification.DiscardClient{})
	return s
}
func Open(path string) (*Service, error) {
	db, err := database.Open(path)
	if err != nil {
		return nil, err
	}
	return New(db), nil
}
func (s *Service) Close() error { return s.Database.Close() }
func (s *Service) SetNotifier(n notification.Client) {
	if n != nil {
		s.notifier = n
		s.DeviceService.Notifier = n
		s.ConversationService.Notifier = n
		s.RunService.Notifier = n
	}
}
func (s *Service) ConfigureFiles(files *file.Service) {
	s.Files, s.ConversationService.Files, s.Skills.Files = files, files, files
}
func (s *Service) Run(ctx context.Context) {
	if err := s.ConversationService.ReconcileRoundNotifications(ctx); err != nil {
		s.Log.Printf("round notification reconciliation: %v", err)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := s.ConversationService.ReconcileRoundNotifications(ctx); err != nil {
				s.Log.Printf("round notification reconciliation: %v", err)
			}
			s.notifier.KickAll()
		}
	}
}
