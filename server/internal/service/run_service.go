package service

import skillservice "github.com/memory-co/agentuse/server/internal/service/skill"

// RunService owns delivery assignments, accepted runs, raw public events and
// controls. An executionEventConsumer applies conversation effects in its tx.
type RunService struct {
	*serviceRuntime
	devices       *DeviceService
	conversations *ConversationService
	skills        *skillservice.Service
}
