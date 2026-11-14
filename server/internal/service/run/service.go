package run

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

// Service owns the run business operations. Peers are explicit contracts.
type Service struct {
	*core.Runtime
	AgentService        agentService
	ConversationService conversationService
	DeviceService       deviceService
	Skills              *skillservice.Service
}

type agentService interface {
	ReadVersion(ctx context.Context, q core.QueryReader, id string) (agentuse.AgentVersion, error)
}

type conversationService interface {
	GetConversation(ctx context.Context, u core.User, id string) (core.Conversation, error)
	DeliveryChanged(ctx context.Context, conversation string)
	PrepareMessage(ctx context.Context, tx *sql.Tx, p core.Delivery) (protocol.ConversationMessage, error)
	ProjectStatus(ctx context.Context, tx *sql.Tx, conversation string, now int64) error
}

type deviceService interface {
	DeviceByKey(ctx context.Context, key string) (core.Device, error)
	CheckRuntime(ctx context.Context, q core.Querier, deviceID, harness string) error
}
