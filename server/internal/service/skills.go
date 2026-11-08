package service

import (
	"context"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) GetSkillVersion(ctx context.Context, u User, id, version string) (agentuse.SkillVersion, error) {
	return s.Skills.Version(ctx, s.db, u, id, version, false)
}
func (s *Service) SkillForCredential(ctx context.Context, token, id, version string) (agentuse.SkillVersion, error) {
	var count int
	if len(token) > 3 && token[:3] == "dk_" {
		d, err := s.DeviceByKey(ctx, token)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
		err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM message_agents p JOIN agent_version_skills a ON a.agent_version_id=p.agent_version_id WHERE p.device_id=? AND p.status IN ('sent','unknown','rejected','accepted','completed','failed','cancelled') AND a.skill_id=? AND a.skill_version_id=?`, d.ID, id, version).Scan(&count)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
	} else {
		err := s.db.QueryRowContext(ctx, `SELECT count(*)`+store.ActiveWorkTokenFrom+` JOIN agent_version_skills a ON a.agent_version_id=p.agent_version_id WHERE t.token_hash=? AND p.status IN ('sent','accepted') AND a.skill_id=? AND a.skill_version_id=?`, HashSecret(token), id, version).Scan(&count)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
	}
	if count == 0 {
		return agentuse.SkillVersion{}, ErrNotFound
	}
	return s.Skills.Version(ctx, s.db, User{Role: RoleAdmin}, id, version, false)
}

// AgentVersionForCredential authorizes the fixed version before native acceptance.
func (s *Service) AgentVersionForCredential(ctx context.Context, token, id, version string) (agentuse.AgentVersion, error) {
	var count int
	if strings.HasPrefix(token, "dk_") {
		d, err := s.DeviceByKey(ctx, token)
		if err != nil {
			return agentuse.AgentVersion{}, err
		}
		err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM message_agents WHERE device_id=? AND agent_id=? AND agent_version_id=? AND status IN ('sent','unknown','rejected','accepted','completed','failed','cancelled')`, d.ID, id, version).Scan(&count)
		if err != nil {
			return agentuse.AgentVersion{}, err
		}
	} else {
		err := s.db.QueryRowContext(ctx, `SELECT count(*)`+store.ActiveWorkTokenFrom+` WHERE t.token_hash=? AND p.agent_id=? AND p.agent_version_id=? AND p.status IN ('sent','accepted')`, HashSecret(token), id, version).Scan(&count)
		if err != nil {
			return agentuse.AgentVersion{}, err
		}
	}
	if count == 0 {
		return agentuse.AgentVersion{}, ErrNotFound
	}
	v, err := agentVersion(ctx, s.db, version)
	if err == nil && v.AgentID != id {
		return agentuse.AgentVersion{}, ErrNotFound
	}
	return v, err
}
