package service

import skillservice "github.com/memory-co/agentuse/server/internal/service/skill"

// AgentService owns agent identities and immutable configurations. Skill version
// validation uses Skills with the publication transaction supplied by this owner.
type AgentService struct {
	*serviceRuntime
	devices *DeviceService
	works   *WorkService
	skills  *skillservice.Service
}
