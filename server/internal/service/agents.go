package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
	"github.com/memory-co/agentuse/server/internal/store"
)

// agent id 全局唯一，建好后不能改。
var agentIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func cleanAgentName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("agent 名字必填")
	}
	if utf8.RuneCountInString(name) > 64 {
		return "", errors.New("agent 名字最长 64 个字")
	}
	return name, nil
}

func agentWriteErr(err error, id, name string) error {
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), "agents.id"):
		return fmt.Errorf("agent id %q 已被占用", id)
	case strings.Contains(err.Error(), "UNIQUE"):
		return fmt.Errorf("已经有叫 %q 的 agent", name)
	}
	return err
}

// CreateAgent is the internal convenience form for a version-one configuration.
func (s *AgentService) CreateAgent(ctx context.Context, user, id, name, device, harness, description string) (Agent, error) {
	return s.CreateConfiguredAgent(ctx, user, id, agentuse.AgentConfig{Name: name, DeviceID: device, Harness: harness, Description: description})
}
func (s *AgentService) validateAgentConfig(ctx context.Context, tx *sql.Tx, user, id string, c *agentuse.AgentConfig) error {
	var err error
	c.Name, err = cleanAgentName(c.Name)
	if err != nil {
		return err
	}
	c.DeviceID = strings.TrimSpace(c.DeviceID)
	c.Harness = strings.TrimSpace(c.Harness)
	c.Model = strings.TrimSpace(c.Model)
	var one int
	if err = tx.QueryRowContext(ctx, store.CheckRuntimeSelect, c.DeviceID, c.Harness, user).Scan(&one); err != nil {
		return fmt.Errorf("device/harness is not registered for this user")
	}
	if err = s.devices.runtimeErr(ctx, tx, c.DeviceID, c.Harness); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM agents a JOIN agent_versions v ON v.id=a.current_version_id WHERE a.user_id=? AND a.deleted_at IS NULL AND a.id<>? AND v.name=?`, user, id, c.Name).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("已经有叫 %q 的 agent", c.Name)
	}
	if c.Skills == nil {
		c.Skills = []agentuse.SkillRef{}
	}
	if len(c.Skills) > 64 {
		return fmt.Errorf("too many skills (maximum 64)")
	}
	names, ids := map[string]bool{}, map[string]bool{}
	for _, ref := range c.Skills {
		v, e := s.skills.Version(ctx, tx, User{ID: user}, ref.SkillID, ref.SkillVersionID, true)
		if e != nil {
			return e
		}
		key := strings.ToLower(v.Name)
		if names[key] || ids[ref.SkillID] {
			return fmt.Errorf("duplicate skill or native skill name %q", v.Name)
		}
		names[key] = true
		ids[ref.SkillID] = true
	}
	return nil
}
func (s *AgentService) writeAgentVersion(ctx context.Context, tx *sql.Tx, v agentuse.AgentVersion) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_versions(id,agent_id,version,name,description,device_id,harness,model,instructions,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, v.ID, v.AgentID, v.Version, v.Name, v.Description, v.DeviceID, v.Harness, nullIfEmpty(v.Model), v.Instructions, v.CreatedAt)
	if err != nil {
		return err
	}
	for i, ref := range v.Skills {
		if _, err = tx.ExecContext(ctx, `INSERT INTO agent_version_skills(agent_version_id,skill_id,skill_version_id,position) VALUES(?,?,?,?)`, v.ID, ref.SkillID, ref.SkillVersionID, i); err != nil {
			return err
		}
	}
	return nil
}
func projection(id, user string, created, updated int64, v agentuse.AgentVersion) Agent {
	return Agent{ID: id, UserID: user, CurrentVersionID: v.ID, Version: v.Version, Name: v.Name, Description: v.Description, DeviceID: v.DeviceID, Harness: v.Harness, Model: v.Model, Instructions: v.Instructions, Skills: v.Skills, CreatedAt: created, UpdatedAt: updated}
}
func (s *AgentService) CreateConfiguredAgent(ctx context.Context, user, id string, c agentuse.AgentConfig) (Agent, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = randomID("ag_")
	} else if !agentIDRe.MatchString(id) {
		return Agent{}, errors.New("agent id 只能用小写字母、数字、- 和 _,最长 64 个字符")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	if err = s.validateAgentConfig(ctx, tx, user, id, &c); err != nil {
		return Agent{}, err
	}
	now := s.unix()
	v := agentuse.AgentVersion{ID: newUUID(), AgentID: id, Version: 1, AgentConfig: c, CreatedAt: now}
	if _, err = tx.ExecContext(ctx, `INSERT INTO agents(id,user_id,current_version_id,created_at,updated_at) VALUES(?,?,?,?,?)`, id, user, v.ID, now, now); err != nil {
		return Agent{}, agentWriteErr(err, id, c.Name)
	}
	if err = s.writeAgentVersion(ctx, tx, v); err != nil {
		return Agent{}, err
	}
	if err = tx.Commit(); err != nil {
		return Agent{}, err
	}
	s.notifyResource("agents", id, user)
	return projection(id, user, now, now, v), nil
}
func agentVersion(ctx context.Context, q queryReader, id string) (agentuse.AgentVersion, error) {
	var v agentuse.AgentVersion
	err := q.QueryRowContext(ctx, `SELECT id,agent_id,version,name,description,device_id,harness,COALESCE(model,''),instructions,created_at FROM agent_versions WHERE id=?`, id).Scan(&v.ID, &v.AgentID, &v.Version, &v.Name, &v.Description, &v.DeviceID, &v.Harness, &v.Model, &v.Instructions, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return v, err
	}
	rows, err := q.QueryContext(ctx, `SELECT skill_id,skill_version_id FROM agent_version_skills WHERE agent_version_id=? ORDER BY position`, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	v.Skills = []agentuse.SkillRef{}
	for rows.Next() {
		var ref agentuse.SkillRef
		if err = rows.Scan(&ref.SkillID, &ref.SkillVersionID); err != nil {
			return v, err
		}
		v.Skills = append(v.Skills, ref)
	}
	return v, rows.Err()
}
func (s *AgentService) GetAgentVersion(ctx context.Context, u User, id, version string) (agentuse.AgentVersion, error) {
	var owner string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM agents WHERE id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return agentuse.AgentVersion{}, err
	}
	if !u.IsAdmin() && owner != u.ID {
		return agentuse.AgentVersion{}, ErrNotFound
	}
	v, err := agentVersion(ctx, s.db, version)
	if err == nil && v.AgentID != id {
		err = ErrNotFound
	}
	return v, err
}
func (s *AgentService) AgentVersions(ctx context.Context, u User, id string) ([]agentuse.AgentVersion, error) {
	if _, err := s.GetAgent(ctx, u, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM agent_versions WHERE agent_id=? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []agentuse.AgentVersion{}
	for _, id := range ids {
		v, e := agentVersion(ctx, s.db, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *AgentService) PublishAgentVersion(ctx context.Context, u User, id, base string, c agentuse.AgentConfig) (Agent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	var owner, head string
	var created, updated int64
	err = tx.QueryRowContext(ctx, `SELECT user_id,current_version_id,created_at,updated_at FROM agents WHERE id=? AND deleted_at IS NULL`, id).Scan(&owner, &head, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if !u.IsAdmin() && u.ID != owner {
		return Agent{}, ErrNotFound
	}
	if base == "" || base != head {
		return Agent{}, skillservice.ErrConflict
	}
	if err = s.validateAgentConfig(ctx, tx, owner, id, &c); err != nil {
		return Agent{}, err
	}
	old, err := agentVersion(ctx, tx, head)
	if err != nil {
		return Agent{}, err
	}
	if reflect.DeepEqual(c, old.AgentConfig) {
		s.notifyResource("agents", id, owner)
		return projection(id, owner, created, updated, old), nil
	}
	now := s.unix()
	v := agentuse.AgentVersion{ID: newUUID(), AgentID: id, Version: old.Version + 1, AgentConfig: c, CreatedAt: now}
	if err = s.writeAgentVersion(ctx, tx, v); err != nil {
		return Agent{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agents SET current_version_id=?,updated_at=? WHERE id=?`, v.ID, now, id); err != nil {
		return Agent{}, err
	}
	if err = tx.Commit(); err != nil {
		return Agent{}, err
	}
	s.notifyResource("agents", id, owner)
	return projection(id, owner, created, now, v), nil
}

func (s *AgentService) DeleteAgent(ctx context.Context, u User, id string) error {
	a, err := s.GetAgent(ctx, u, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE agents SET deleted_at=?,updated_at=? WHERE id=?`, s.unix(), s.unix(), id); err != nil {
		return err
	}
	if err = s.works.detachCoordinatorAgent(ctx, tx, id); err != nil {
		return err
	}
	err = tx.Commit()
	if err == nil {
		s.notifyResource("agents", id, a.UserID)
		s.notifyResource("works", "", a.UserID)
	}
	return err
}
func (s *AgentService) GetAgent(ctx context.Context, u User, id string) (Agent, error) {
	q := agentCols + ` WHERE id=?`
	args := []any{id}
	if !u.IsAdmin() {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	a, err := scanAgent(s.db.QueryRowContext(ctx, q, args...))
	if err != nil {
		return a, err
	}
	v, err := agentVersion(ctx, s.db, a.CurrentVersionID)
	a.Skills = v.Skills
	return a, err
}
func (s *AgentService) ListAgents(ctx context.Context, u User) ([]Agent, error) {
	q := agentCols
	args := []any{}
	if !u.IsAdmin() {
		q += ` WHERE user_id=?`
		args = append(args, u.ID)
	}
	q += ` ORDER BY name,id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []Agent{}
	for rows.Next() {
		a, e := scanAgent(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		v, e := agentVersion(ctx, s.db, out[i].CurrentVersionID)
		if e != nil {
			return nil, e
		}
		out[i].Skills = v.Skills
	}
	return out, nil
}
