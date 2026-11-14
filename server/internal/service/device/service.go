package device

import (
	"context"
	"database/sql"
	"time"

	"github.com/memory-co/agentuse/server/internal/notification"
)

// Service owns the device business operations. Peers are explicit contracts.
type Service struct {
	DB       *sql.DB
	Now      func() time.Time
	Changes  *notification.Broker
	Notifier interface {
		Disconnect(string)
		KickUser(string)
	}
	ConversationService conversationService
}

type conversationService interface {
	DetachDeviceAttachments(ctx context.Context, tx *sql.Tx, device string) error
	NotifyDeviceConversations(context.Context, string)
	RetireDeviceMessages(ctx context.Context, tx *sql.Tx, id, reason string) error
}
