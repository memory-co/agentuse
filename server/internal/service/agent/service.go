package agent

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/server/internal/notification"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

// Service owns the agent business operations. Peers are explicit contracts.
type Service struct {
	DB                  *gorm.DB
	Now                 func() time.Time
	Changes             *notification.Broker
	ConversationService interface{ ParentAgentDeleted(context.Context, string) }
	DeviceService       deviceService
	Skills              *skillservice.Service
}

type deviceService interface {
	CheckRuntime(ctx context.Context, q *gorm.DB, deviceID, harness string) error
}
