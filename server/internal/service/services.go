// Package service assembles the Server's business services.
package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/memory-co/agentuse/internal/access"
	"github.com/memory-co/agentuse/internal/securews"
	"log"
	"net/url"
	"path/filepath"
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
	"github.com/memory-co/agentuse/server/internal/service/upgrade"
	"github.com/memory-co/agentuse/server/internal/service/user"
)

type Service struct {
	Security            *SecurityState
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
	UpgradeService      *upgrade.Service
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
	s.UpgradeService = upgrade.New(s.DB, upgrade.Config{Now: now, Changes: s.BrowserChanges})
	s.DeviceService.ConversationService = s.ConversationService
	s.AgentService.DeviceService, s.AgentService.ConversationService = s.DeviceService, s.ConversationService
	s.ConversationService.RunService = s.RunService
	s.ConversationService.DeviceService = s.DeviceService
	s.RunService.ConversationService, s.RunService.AgentService = s.ConversationService, s.AgentService
	s.Skills = &skill.Service{DB: s.DB, Now: now, OnChange: func(id, owner string) {
		s.BrowserChanges.Publish(notification.Change{Resource: "skills", ID: id}, owner)
	}}
	s.AgentService.Skills, s.RunService.Skills = s.Skills, s.Skills
	s.SetNotifier(notification.DiscardClient{})
	if err := s.ConfigureSecurity(SecurityConfig{}); err != nil {
		panic(err)
	}
	return s
}
func Open(path string) (*Service, error) {
	db, err := database.Open(path)
	if err != nil {
		return nil, err
	}
	return New(db), nil
}
func (s *Service) Close() error { s.UpgradeService.Close(); return s.Database.Close() }

func (s *Service) ConfigureUpgrade(config upgrade.Config) error {
	s.UpgradeService.Close()
	config.Now = func() time.Time { return s.Now() }
	config.Changes = s.BrowserChanges
	s.UpgradeService = upgrade.New(s.DB, config)
	return s.UpgradeService.Recover(context.Background())
}
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

type SecurityConfig struct {
	Directory      string
	PublicURL      string
	WSEncryption   *bool
	PrepareTimeout time.Duration
	RunTimeout     time.Duration
}

// SecurityState is configuration and cryptographic material, not a business
// service. Device and run owners retain all database authorization decisions.
type SecurityState struct {
	Identity     *securews.Identity
	WSEncryption bool
}

func (s *Service) ConfigureSecurity(config SecurityConfig) error {
	var id *securews.Identity
	var key []byte
	var err error
	if config.Directory == "" {
		id, err = securews.NewIdentity(s.Now())
		key = make([]byte, 32)
		if err == nil {
			_, err = rand.Read(key)
		}
	} else {
		id, err = securews.LoadIdentity(config.Directory, s.Now())
		if err == nil {
			key, err = access.LoadKey(filepath.Join(config.Directory, "access-signing.key"))
		}
	}
	if err != nil {
		return err
	}
	encrypted := true
	if config.WSEncryption != nil {
		encrypted = *config.WSEncryption
	}
	if !encrypted {
		u, e := url.Parse(config.PublicURL)
		if e != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("disabling WS inner encryption requires an HTTPS public_url")
		}
	}
	signer, err := access.New(key, securews.ServerID(id.CA))
	if err != nil {
		return err
	}
	if config.PrepareTimeout == 0 {
		config.PrepareTimeout = 30 * time.Minute
	}
	if config.RunTimeout == 0 {
		config.RunTimeout = 24 * time.Hour
	}
	if config.PrepareTimeout < time.Second || config.RunTimeout < time.Second {
		return fmt.Errorf("security access timeouts must be positive")
	}
	s.Security = &SecurityState{Identity: id, WSEncryption: encrypted}
	s.DeviceService.Identity = id
	s.ConversationService.AccessSigner = signer
	s.RunService.AccessSigner = signer
	s.ConversationService.PrepareTimeout = config.PrepareTimeout
	s.RunService.PrepareTimeout = config.PrepareTimeout
	s.ConversationService.AccessTimeout = config.RunTimeout
	s.RunService.AccessTimeout = config.RunTimeout
	return nil
}
