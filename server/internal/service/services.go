// Package service assembles the Server's business services.
package service

import (
	"context"
	"time"

	"github.com/memory-co/agentuse/server/internal/service/agent"
	"github.com/memory-co/agentuse/server/internal/service/conversation"
	"github.com/memory-co/agentuse/server/internal/service/device"
	"github.com/memory-co/agentuse/server/internal/service/file"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/service/run"
	"github.com/memory-co/agentuse/server/internal/service/skill"
	"github.com/memory-co/agentuse/server/internal/service/user"
	"github.com/memory-co/agentuse/server/internal/store"
)

type UserService = user.Service
type DeviceService = device.Service
type AgentService = agent.Service
type ConversationService = conversation.Service
type RunService = run.Service

// Service composes independent packages; business operations live in their owner.
type Service struct {
	*core.Runtime
	*UserService
	*DeviceService
	*AgentService
	*ConversationService
	*RunService
	Files  *file.Service
	Skills *skill.Service
}

func New(st *store.Store) *Service {
	runtime := core.NewRuntime(st)
	s := &Service{
		Runtime:             runtime,
		UserService:         &user.Service{Runtime: runtime},
		DeviceService:       &device.Service{Runtime: runtime},
		AgentService:        &agent.Service{Runtime: runtime},
		ConversationService: &conversation.Service{Runtime: runtime},
		RunService:          &run.Service{Runtime: runtime},
	}
	s.DeviceService.RunService, s.DeviceService.ConversationService = s.RunService, s.ConversationService
	s.AgentService.DeviceService = s.DeviceService
	s.AgentService.ConversationService = s.ConversationService
	s.ConversationService.RunService = s.RunService
	s.RunService.DeviceService, s.RunService.ConversationService, s.RunService.AgentService = s.DeviceService, s.ConversationService, s.AgentService
	s.Skills = &skill.Service{OnChange: func(id, owner string) { runtime.NotifyResource("skills", id, owner) }, DB: runtime.DB, Now: func() time.Time { return runtime.Now() }}
	s.AgentService.Skills, s.RunService.Skills = s.Skills, s.Skills
	return s
}

func Open(path string) (*Service, error) {
	st, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	return New(st), nil
}

func (s *Service) Close() error { return s.Repository.Close() }

func (s *Service) SetNotifier(n Notifier) {
	if n != nil {
		s.Notifier = n
	}
}

func (s *Service) ConfigureFiles(files *file.Service) {
	s.Files, s.ConversationService.Files, s.Skills.Files = files, files, files
}

func (s *Service) Run(ctx context.Context) {
	if err := s.ConversationService.ReconcileParentMessages(ctx); err != nil {
		s.Log.Printf("parent message reconciliation: %v", err)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := s.ConversationService.ReconcileParentMessages(ctx); err != nil {
				s.Log.Printf("parent message reconciliation: %v", err)
			}
			s.Notifier.KickAll()
		}
	}
}

// Public model aliases keep the composition API stable.
type Agent = core.Agent
type AttachmentAccess = core.AttachmentAccess
type BrowserChange = core.BrowserChange
type BrowserChanges = core.BrowserChanges
type BrowserSubscription = core.BrowserSubscription
type Conversation = core.Conversation
type ConversationDetail = core.ConversationDetail
type ConversationEvent = core.ConversationEvent

const ConversationIdle = core.ConversationIdle
const ConversationPending = core.ConversationPending
const ConversationResolved = core.ConversationResolved
const ConversationRunning = core.ConversationRunning
const ConversationWaiting = core.ConversationWaiting

type Delivery = core.Delivery
type Device = core.Device
type DeviceHarness = core.DeviceHarness

const DeviceOffline = core.DeviceOffline
const DeviceOnline = core.DeviceOnline
const DevicePending = core.DevicePending

var ErrAttachmentCapability = core.ErrAttachmentCapability
var ErrAttachmentCursor = core.ErrAttachmentCursor
var ErrAttachmentPathConflict = core.ErrAttachmentPathConflict
var ErrAttachmentQuota = core.ErrAttachmentQuota
var ErrAttachmentReferenced = core.ErrAttachmentReferenced
var ErrAttachmentRun = core.ErrAttachmentRun
var ErrAttachmentUnauthorized = core.ErrAttachmentUnauthorized
var ErrConversationBusy = core.ErrConversationBusy
var ErrConversationState = core.ErrConversationState
var ErrNotFound = core.ErrNotFound
var ErrQuestionClosed = core.ErrQuestionClosed
var ErrRuntimeUnavailable = core.ErrRuntimeUnavailable

type Harness = core.Harness

var HashSecret = core.HashSecret

type Message = core.Message
type Notifier = core.Notifier
type Question = core.Question

var RandomToken = core.RandomToken

type Reply = core.Reply
type ReportedHarness = core.ReportedHarness

const RoleAdmin = core.RoleAdmin
const RoleAgentMsg = core.RoleAgentMsg
const RoleUser = core.RoleUser
const RoleUserMsg = core.RoleUserMsg

type Run = core.Run

const RunCapability = core.RunCapability

type User = core.User

type AgentSource = core.AgentSource

var ErrConversationScope = core.ErrConversationScope
var ErrConversationChildren = core.ErrConversationChildren
