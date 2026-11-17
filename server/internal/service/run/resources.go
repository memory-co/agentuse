package run

import (
	"context"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

func (s *Service) SkillForCredential(ctx context.Context, token, id, version string) (agentuse.SkillVersion, error) {
	p, err := s.AccessForRequest(ctx, token)
	if err != nil {
		return agentuse.SkillVersion{}, ErrAccess
	}
	var count int
	err = database.Row(ctx, s.DB, "SELECT count(*) FROM agent_version_skills WHERE agent_version_id=? AND skill_id=? AND skill_version_id=?", p.Claims.AgentVersionID, id, version).Scan(&count)
	if err != nil {
		return agentuse.SkillVersion{}, err
	}
	if count != 1 {
		return agentuse.SkillVersion{}, database.ErrNotFound
	}
	return s.Skills.Version(ctx, s.DB, agentuse.User{Role: agentuse.RoleAdmin}, id, version, false)
}

func (s *Service) AgentVersionForCredential(ctx context.Context, token, id, version string) (agentuse.AgentVersion, error) {
	p, err := s.AccessForRequest(ctx, token)
	if err != nil {
		return agentuse.AgentVersion{}, ErrAccess
	}
	if p.Claims.AgentID != id || p.Claims.AgentVersionID != version {
		return agentuse.AgentVersion{}, database.ErrNotFound
	}
	return s.AgentService.ReadVersion(ctx, s.DB, version)
}
