package device

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

// Service owns the device business operations. Peers are explicit contracts.
type Service struct {
	*core.Runtime
	ConversationService conversationService
	RunService          runService
	WorkService         workService
}

type conversationService interface {
	DetachDeviceAttachments(ctx context.Context, tx *sql.Tx, device string) error
}

type runService interface {
	RetireDeviceDeliveries(ctx context.Context, tx *sql.Tx, id, reason string) error
}

type workService interface {
	ReconcileWorkNotifications(ctx context.Context) error
}
