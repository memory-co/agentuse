package run

import (
	"context"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

func (s *Service) SkillForCredential(ctx context.Context, token, id, version string) (agentuse.SkillVersion, error) {
	tq := database.MessageTargets(s.DB, "m", "t")

	var count int
	if len(token) > 3 && token[:3] == "dk_" {
		d, err := s.DeviceService.DeviceByKey(ctx, token)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
		err = database.Row(ctx, s.DB, `SELECT count(*) FROM `+tq.From+` JOIN agent_version_skills a ON a.agent_version_id=`+tq.Version+` WHERE `+tq.Device+`=? AND `+tq.Dispatched+` AND a.skill_id=? AND a.skill_version_id=?`, d.ID, id, version).Scan(&count)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
	} else {
		return agentuse.SkillVersion{}, database.ErrNotFound
	}
	if count == 0 {
		return agentuse.SkillVersion{}, database.ErrNotFound
	}
	return s.Skills.Version(ctx, s.DB, agentuse.User{Role: agentuse.RoleAdmin}, id, version, false)
}

// AgentVersionForCredential authorizes the fixed version before native acceptance.
func (s *Service) AgentVersionForCredential(ctx context.Context, token, id, version string) (agentuse.AgentVersion, error) {
	tq := database.MessageTargets(s.DB, "m", "t")

	var count int
	if strings.HasPrefix(token, "dk_") {
		d, err := s.DeviceService.DeviceByKey(ctx, token)
		if err != nil {
			return agentuse.AgentVersion{}, err
		}
		err = database.Row(ctx, s.DB, `SELECT count(*) FROM `+tq.From+` WHERE `+tq.Device+`=? AND `+tq.Key+`=? AND `+tq.Version+`=? AND `+tq.Dispatched, d.ID, id, version).Scan(&count)
		if err != nil {
			return agentuse.AgentVersion{}, err
		}
	} else {
		return agentuse.AgentVersion{}, database.ErrNotFound
	}
	if count == 0 {
		return agentuse.AgentVersion{}, database.ErrNotFound
	}
	v, err := s.AgentService.ReadVersion(ctx, s.DB, version)
	if err == nil && v.AgentID != id {
		return agentuse.AgentVersion{}, database.ErrNotFound
	}
	return v, err
}
