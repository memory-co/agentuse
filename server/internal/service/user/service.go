package user

import (
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/server/internal/notification"
)

// Service owns the user business operations. Peers are explicit contracts.
type Service struct {
	DB      *gorm.DB
	Now     func() time.Time
	Changes *notification.Broker
}
