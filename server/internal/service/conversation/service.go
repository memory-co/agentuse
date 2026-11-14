package conversation

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
	"github.com/memory-co/agentuse/server/internal/service/run"
	"github.com/memory-co/agentuse/server/internal/store"
)

// Service owns the conversation business operations. Peers are explicit contracts.
type Service struct {
	DB            *sql.DB
	Repository    *store.Store
	Now           func() time.Time
	Log           *log.Logger
	Changes       *notification.Broker
	Notifier      interface{ KickAll() }
	RunService    runService
	DeviceService interface {
		CheckRuntime(context.Context, store.Querier, string, string) error
	}
	Files                        *fileservice.Service
	conversationAttachmentLimits conversationAttachmentLimits
	conversationFilesMu          sync.RWMutex
}

type runService interface {
	ApplyClientEvent(ctx context.Context, device string, envelope protocol.ClientEvent, consumer run.ExecutionEventConsumer) error
	DeleteConversationExecution(ctx context.Context, tx *sql.Tx, id string) error
	DispatchedTarget(ctx context.Context, q store.Querier, device, conversation, message, agent string) (run.MessageTarget, error)
	RunInputsIn(ctx context.Context, q store.RowsQuerier, conversation string) ([]agentuse.RunInput, error)
	EventsIn(ctx context.Context, q store.RowsQuerier, conversation string) ([]agentuse.ConversationEvent, error)
	LinkRunReply(ctx context.Context, tx *sql.Tx, run, message string) error
	QuestionsIn(ctx context.Context, q store.RowsQuerier, where string, args ...any) ([]agentuse.Question, error)
	RunsIn(ctx context.Context, q store.RowsQuerier, conversation string) ([]agentuse.Run, error)
}
