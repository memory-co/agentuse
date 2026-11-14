package conversation

import (
	"context"
	"database/sql"
	"sync"

	"github.com/memory-co/agentuse/internal/protocol"
	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/service/run"
)

// Service owns the conversation business operations. Peers are explicit contracts.
type Service struct {
	*core.Runtime
	RunService                   runService
	WorkService                  workService
	Files                        *fileservice.Service
	conversationAttachmentLimits conversationAttachmentLimits
	conversationFilesMu          sync.RWMutex
}

type runService interface {
	ApplyClientEvent(ctx context.Context, device string, envelope protocol.ClientEvent, consumer run.ExecutionEventConsumer) error
	AssignMessageTargets(ctx context.Context, tx *sql.Tx, conversation core.Conversation, message string, agentIDs []string) error
	DeleteConversationExecution(ctx context.Context, tx *sql.Tx, id string) error
	DeliveredTarget(ctx context.Context, q core.Querier, device, conversation, message, agent string) (core.Delivery, error)
	DeliveriesIn(ctx context.Context, q core.RowsQuerier, where string, args ...any) ([]core.Delivery, error)
	EventsIn(ctx context.Context, q core.RowsQuerier, conversation string) ([]core.ConversationEvent, error)
	LinkRunReply(ctx context.Context, tx *sql.Tx, run, message string) error
	QuestionsIn(ctx context.Context, q core.RowsQuerier, where string, args ...any) ([]core.Question, error)
	RunsIn(ctx context.Context, q core.RowsQuerier, conversation string) ([]core.Run, error)
}

type workService interface {
	ConversationInstructions(ctx context.Context, tx *sql.Tx, conversation core.Conversation, agent string) (string, error)
	DeleteWorkConversationReferences(ctx context.Context, tx *sql.Tx, id string) error
	LookupWork(ctx context.Context, q core.Querier, u core.User, id string) (core.Work, error)
	ReconcileWorkConversation(ctx context.Context, conversation string) error
}
