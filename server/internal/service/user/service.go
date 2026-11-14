package user

import (
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

// Service owns the user business operations. Peers are explicit contracts.
type Service struct {
	*core.Runtime
}
