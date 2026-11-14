package agent

import (
	"context"
	"database/sql"
	"time"

	"github.com/memory-co/agentuse/server/internal/notification"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
	"github.com/memory-co/agentuse/server/internal/store"
)

// Service owns the agent business operations. Peers are explicit contracts.
type Service struct {
	DB                  *sql.DB
	Now                 func() time.Time
	Changes             *notification.Broker
	ConversationService interface{ ParentAgentDeleted(context.Context, string) }
	DeviceService       deviceService
	Skills              *skillservice.Service
}

type deviceService interface {
	CheckRuntime(ctx context.Context, q store.Querier, deviceID, harness string) error
}
