package run

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
	"github.com/memory-co/agentuse/server/internal/store"
)

// Service owns the run business operations. Peers are explicit contracts.
type Service struct {
	DB         *gorm.DB
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
	ReadVersion(ctx context.Context, q *gorm.DB, id string) (agentuse.AgentVersion, error)
}

type conversationService interface {
	NotifyConversation(context.Context, string)
	GetConversation(ctx context.Context, u agentuse.User, id string) (agentuse.Conversation, error)
	DeliveryChanged(ctx context.Context, conversation string)
	PrepareMessage(ctx context.Context, tx *gorm.DB, p MessageTarget) (protocol.ConversationMessage, error)
	SaveMessageDispatch(context.Context, *gorm.DB, string, string, protocol.ConversationMessage) error
	SaveMessageError(context.Context, *gorm.DB, string, string, *agentuse.MessageError) error
	ProjectStatus(ctx context.Context, tx *gorm.DB, conversation string, now int64) error
}

type deviceService interface {
	DeviceByKey(ctx context.Context, key string) (agentuse.Device, error)
}
