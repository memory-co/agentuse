package run

import (
	"context"
	"database/sql"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
	"github.com/memory-co/agentuse/server/internal/store"
)

// Service owns the run business operations. Peers are explicit contracts.
type Service struct {
	DB         *sql.DB
	Repository *store.Store
	Now        func() time.Time
	Notifier   interface {
		PushRun(string, protocol.Message)
	}
	AgentService        agentService
	ConversationService conversationService
	DeviceService       deviceService
	Skills              *skillservice.Service
}

type agentService interface {
	ReadVersion(ctx context.Context, q store.QueryReader, id string) (agentuse.AgentVersion, error)
}

type conversationService interface {
	NotifyConversation(context.Context, string)
	GetConversation(ctx context.Context, u agentuse.User, id string) (agentuse.Conversation, error)
	DeliveryChanged(ctx context.Context, conversation string)
	PrepareMessage(ctx context.Context, tx *sql.Tx, p MessageTarget) (protocol.ConversationMessage, error)
	SaveMessageDispatch(context.Context, *sql.Tx, string, string, protocol.ConversationMessage) error
	SaveMessageError(context.Context, *sql.Tx, string, string, *agentuse.MessageError) error
	ProjectStatus(ctx context.Context, tx *sql.Tx, conversation string, now int64) error
}

type deviceService interface {
	DeviceByKey(ctx context.Context, key string) (agentuse.Device, error)
}
