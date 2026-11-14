// Package service coordinates server-side business operations and notifications.
package service

import (
	"database/sql"
	"log"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
	"github.com/memory-co/agentuse/server/internal/store"
)

type Notifier interface {
	KickAll()
	KickUser(string)
	PushRun(string, protocol.Message)
	Disconnect(string)
}

// Service is the Server composition root. Promoted methods preserve the HTTP and
// WebSocket API while each business operation belongs to its domain service.
type Service struct {
	*serviceRuntime
	*UserService
	*DeviceService
	*AgentService
	*ConversationService
	*RunService
	*WorkService
	Files  *fileservice.Service
	Skills *skillservice.Service
}

// serviceRuntime contains shared infrastructure, never a business service or a
// back-reference to the composition root. Peers are wired explicitly below.
type serviceRuntime struct {
	BrowserChanges BrowserChanges
	log            *log.Logger
	repository     *store.Store
	db             *sql.DB
	now            func() time.Time
	Notifier       Notifier
}

func New(st *store.Store) *Service {
	runtime := &serviceRuntime{log: log.Default(), repository: st, db: st.Connection(), now: time.Now, Notifier: discardNotifier{}}
	s := &Service{
		serviceRuntime:      runtime,
		UserService:         &UserService{serviceRuntime: runtime},
		DeviceService:       &DeviceService{serviceRuntime: runtime},
		AgentService:        &AgentService{serviceRuntime: runtime},
		ConversationService: &ConversationService{serviceRuntime: runtime},
		RunService:          &RunService{serviceRuntime: runtime},
		WorkService:         &WorkService{serviceRuntime: runtime},
	}
	s.DeviceService.runs, s.DeviceService.works, s.DeviceService.conversations = s.RunService, s.WorkService, s.ConversationService
	s.AgentService.devices, s.AgentService.works = s.DeviceService, s.WorkService
	s.ConversationService.runs, s.ConversationService.works = s.RunService, s.WorkService
	s.RunService.devices, s.RunService.conversations = s.DeviceService, s.ConversationService
	s.WorkService.users, s.WorkService.devices, s.WorkService.conversations = s.UserService, s.DeviceService, s.ConversationService
	s.Skills = &skillservice.Service{OnChange: func(id, owner string) { runtime.notifyResource("skills", id, owner) }, DB: runtime.db, Now: func() time.Time { return runtime.now() }}
	s.AgentService.skills, s.RunService.skills = s.Skills, s.Skills
	return s
}
func Open(path string) (*Service, error) {
	st, e := store.Open(path)
	if e != nil {
		return nil, e
	}
	return New(st), nil
}
func (s *Service) Close() error       { return s.repository.Close() }
func (s *serviceRuntime) unix() int64 { return s.now().Unix() }
func (s *Service) SetNotifier(n Notifier) {
	if n != nil {
		s.Notifier = n
	}
}

type discardNotifier struct{}

func (discardNotifier) KickAll()                         {}
func (discardNotifier) KickUser(string)                  {}
func (discardNotifier) PushRun(string, protocol.Message) {}

func (discardNotifier) Disconnect(string) {}

func (s *Service) ConfigureFiles(files *fileservice.Service) {
	s.Files, s.ConversationService.files, s.Skills.Files = files, files, files
}
