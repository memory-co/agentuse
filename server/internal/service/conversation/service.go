package conversation

import (
	"context"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
	"github.com/memory-co/agentuse/server/internal/service/run"
	"github.com/memory-co/agentuse/server/internal/store"
)

// Service owns the conversation business operations. Peers are explicit contracts.
type Service struct {
	DB            *gorm.DB
	Repository    *store.Store
	Now           func() time.Time
	Log           *log.Logger
	Changes       *notification.Broker
	Notifier      interface{ KickAll() }
	RunService    runService
	DeviceService interface {
		CheckRuntime(context.Context, *gorm.DB, string, string) error
	}
	Files                        *fileservice.Service
	conversationAttachmentLimits conversationAttachmentLimits
	conversationFilesMu          sync.RWMutex
}

type runService interface {
	ApplyClientEvent(ctx context.Context, device string, envelope protocol.ClientEvent, consumer run.ExecutionEventConsumer) error
	DeleteConversationExecution(ctx context.Context, tx *gorm.DB, id string) error
	DispatchedTarget(ctx context.Context, q *gorm.DB, device, conversation, message, agent string) (run.MessageTarget, error)
	RunInputsIn(ctx context.Context, q *gorm.DB, conversation string) ([]agentuse.RunInput, error)
	EventsIn(ctx context.Context, q *gorm.DB, conversation string) ([]agentuse.ConversationEvent, error)
	LinkRunReply(ctx context.Context, tx *gorm.DB, run, message string) error
	QuestionsIn(ctx context.Context, q *gorm.DB, where string, args ...any) ([]agentuse.Question, error)
	RunsIn(ctx context.Context, q *gorm.DB, conversation string) ([]agentuse.Run, error)
}
