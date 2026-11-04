package service

import (
	"context"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func (s *Service) GetSkillVersion(ctx context.Context, u User, id, version string) (agentuse.SkillVersion, error) {
	return s.Skills.Version(ctx, s.db, u, id, version, false)
}
func (s *Service) SkillForCredential(ctx context.Context, token, id, version string) (agentuse.SkillVersion, error) {
	var count int
	if len(token) > 3 && token[:3] == "dk_" {
		d, err := s.DaemonByKey(ctx, token)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
		err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM run_prompts p JOIN agent_version_skills a ON a.agent_version_id=p.agent_version_id WHERE p.daemon_id=? AND p.status IN ('sent','accepted','completed','failed','cancelled') AND a.skill_id=? AND a.skill_version_id=?`, d.ID, id, version).Scan(&count)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
	} else {
		err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM work_run_tokens t JOIN run_prompts p ON p.id=t.prompt_id JOIN agent_version_skills a ON a.agent_version_id=p.agent_version_id WHERE t.token_hash=? AND p.status IN ('sent','accepted') AND a.skill_id=? AND a.skill_version_id=?`, HashSecret(token), id, version).Scan(&count)
		if err != nil {
			return agentuse.SkillVersion{}, err
		}
	}
	if count == 0 {
		return agentuse.SkillVersion{}, ErrNotFound
	}
	return s.Skills.Version(ctx, s.db, User{Role: RoleAdmin}, id, version, false)
}
