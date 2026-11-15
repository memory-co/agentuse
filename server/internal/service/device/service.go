package device

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/server/internal/notification"
)

// Service owns the device business operations. Peers are explicit contracts.
type Service struct {
	DB       *gorm.DB
	Now      func() time.Time
	Changes  *notification.Broker
	Notifier interface {
		Disconnect(string)
		KickUser(string)
	}
	ConversationService conversationService
}

type conversationService interface {
	DetachDeviceAttachments(ctx context.Context, tx *gorm.DB, device string) error
	NotifyDeviceConversations(context.Context, string)
	RetireDeviceMessages(ctx context.Context, tx *gorm.DB, id, reason string) error
}
