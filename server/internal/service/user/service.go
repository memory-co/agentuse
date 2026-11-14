package user

import (
	"database/sql"
	"time"

	"github.com/memory-co/agentuse/server/internal/notification"
)

// Service owns the user business operations. Peers are explicit contracts.
type Service struct {
	DB      *sql.DB
	Now     func() time.Time
	Changes *notification.Broker
}
