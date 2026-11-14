package agent

import (
	"context"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

// Service owns the agent business operations. Peers are explicit contracts.
type Service struct {
	*core.Runtime
	ConversationService interface{ ParentAgentDeleted(context.Context, string) }
	DeviceService       deviceService
	Skills              *skillservice.Service
}

type deviceService interface {
	CheckRuntime(ctx context.Context, q core.Querier, deviceID, harness string) error
}
