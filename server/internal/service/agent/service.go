package agent

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

// Service owns the agent business operations. Peers are explicit contracts.
type Service struct {
	*core.Runtime
	DeviceService deviceService
	WorkService   workService
	Skills        *skillservice.Service
}

type deviceService interface {
	CheckRuntime(ctx context.Context, q core.Querier, deviceID, harness string) error
}

type workService interface {
	DetachCoordinatorAgent(ctx context.Context, tx *sql.Tx, agent string) error
}
