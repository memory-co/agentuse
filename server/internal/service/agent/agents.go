package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/identity"

	"gorm.io/gorm"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/notification"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
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

func agentWriteErr(err error, id string) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("agent id %q 已被占用", id)
	}
	return err
}

// CreateAgent is the internal convenience form for a version-one configuration.
func (s *Service) CreateAgent(ctx context.Context, user, id, name, device, harness, description string) (agentuse.Agent, error) {
	return s.CreateConfiguredAgent(ctx, user, id, agentuse.AgentConfig{Name: name, DeviceID: device, Harness: harness, Description: description})
}
func (s *Service) validateAgentConfig(ctx context.Context, tx *gorm.DB, user, id string, c *agentuse.AgentConfig) error {
	var err error
	c.Name, err = cleanAgentName(c.Name)
	if err != nil {
		return err
	}
	c.DeviceID = strings.TrimSpace(c.DeviceID)
	c.Harness = strings.TrimSpace(c.Harness)
	c.Model = strings.TrimSpace(c.Model)
	var one int
	if err = database.Row(ctx, tx, database.CheckRuntimeSelect, c.DeviceID, c.Harness, user).Scan(&one); err != nil {
		return fmt.Errorf("device/harness is not registered for this user")
	}
	if err = s.DeviceService.CheckRuntime(ctx, tx, c.DeviceID, c.Harness); err != nil {
		return err
	}
	var count int
	if err = database.Row(ctx, tx, `SELECT count(*) FROM agents a JOIN agent_versions v ON v.id=a.current_version_id WHERE a.user_id=? AND a.deleted_at IS NULL AND a.id<>? AND v.name=?`, user, id, c.Name).Scan(&count); err != nil {
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
		v, e := s.Skills.Version(ctx, tx, agentuse.User{ID: user}, ref.SkillID, ref.SkillVersionID, true)
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
func (s *Service) writeAgentVersion(ctx context.Context, tx *gorm.DB, v agentuse.AgentVersion) error {
	_, err := database.Exec(ctx, tx, `INSERT INTO agent_versions(id,agent_id,version,name,description,device_id,harness,model,instructions,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, v.ID, v.AgentID, v.Version, v.Name, v.Description, v.DeviceID, v.Harness, database.NullIfEmpty(v.Model), v.Instructions, v.CreatedAt)
	if err != nil {
		return err
	}
	for i, ref := range v.Skills {
		if _, err = database.Exec(ctx, tx, `INSERT INTO agent_version_skills(agent_version_id,skill_id,skill_version_id,position) VALUES(?,?,?,?)`, v.ID, ref.SkillID, ref.SkillVersionID, i); err != nil {
			return err
		}
	}
	return nil
}
func projection(id, user string, created, updated int64, v agentuse.AgentVersion) agentuse.Agent {
	return agentuse.Agent{ID: id, UserID: user, CurrentVersionID: v.ID, Version: v.Version, Name: v.Name, Description: v.Description, DeviceID: v.DeviceID, Harness: v.Harness, Model: v.Model, Instructions: v.Instructions, Skills: v.Skills, CreatedAt: created, UpdatedAt: updated}
}
func (s *Service) CreateConfiguredAgent(ctx context.Context, user, id string, c agentuse.AgentConfig) (agentuse.Agent, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = identity.NewID("ag_")
	} else if !agentIDRe.MatchString(id) {
		return agentuse.Agent{}, errors.New("agent id 只能用小写字母、数字、- 和 _,最长 64 个字符")
	}
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return agentuse.Agent{}, err
	}
	defer tx.Rollback()
	if err = s.validateAgentConfig(ctx, tx, user, id, &c); err != nil {
		return agentuse.Agent{}, err
	}
	now := s.Now().Unix()
	v := agentuse.AgentVersion{ID: uuid.NewString(), AgentID: id, Version: 1, AgentConfig: c, CreatedAt: now}
	if _, err = database.Exec(ctx, tx, `INSERT INTO agents(id,user_id,current_version_id,created_at,updated_at) VALUES(?,?,?,?,?)`, id, user, v.ID, now, now); err != nil {
		return agentuse.Agent{}, agentWriteErr(err, id)
	}
	if err = s.writeAgentVersion(ctx, tx, v); err != nil {
		return agentuse.Agent{}, err
	}
	if err = tx.Commit().Error; err != nil {
		return agentuse.Agent{}, err
	}
	s.Changes.Publish(notification.Change{Resource: "agents", ID: id}, user)
	return projection(id, user, now, now, v), nil
}
func (s *Service) ReadVersion(ctx context.Context, q *gorm.DB, id string) (agentuse.AgentVersion, error) {
	var v agentuse.AgentVersion
	err := database.Row(ctx, q, `SELECT id,agent_id,version,name,description,device_id,harness,COALESCE(model,''),instructions,created_at FROM agent_versions WHERE id=?`, id).Scan(&v.ID, &v.AgentID, &v.Version, &v.Name, &v.Description, &v.DeviceID, &v.Harness, &v.Model, &v.Instructions, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = database.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	rows, err := database.Rows(ctx, q, `SELECT skill_id,skill_version_id FROM agent_version_skills WHERE agent_version_id=? ORDER BY position`, id)
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
func (s *Service) GetAgentVersion(ctx context.Context, u agentuse.User, id, version string) (agentuse.AgentVersion, error) {
	var owner string
	err := database.Row(ctx, s.DB, `SELECT user_id FROM agents WHERE id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		err = database.ErrNotFound
	}
	if err != nil {
		return agentuse.AgentVersion{}, err
	}
	if !u.IsAdmin() && owner != u.ID {
		return agentuse.AgentVersion{}, database.ErrNotFound
	}
	v, err := s.ReadVersion(ctx, s.DB, version)
	if err == nil && v.AgentID != id {
		err = database.ErrNotFound
	}
	return v, err
}
func (s *Service) AgentVersions(ctx context.Context, u agentuse.User, id string) ([]agentuse.AgentVersion, error) {
	if _, err := s.GetAgent(ctx, u, id); err != nil {
		return nil, err
	}
	rows, err := database.Rows(ctx, s.DB, `SELECT id FROM agent_versions WHERE agent_id=? ORDER BY version DESC`, id)
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
		v, e := s.ReadVersion(ctx, s.DB, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) PublishAgentVersion(ctx context.Context, u agentuse.User, id, base string, c agentuse.AgentConfig) (agentuse.Agent, error) {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return agentuse.Agent{}, err
	}
	defer tx.Rollback()
	var owner, head string
	var created, updated int64
	err = database.Row(ctx, tx, `SELECT user_id,current_version_id,created_at,updated_at FROM agents WHERE id=? AND deleted_at IS NULL`, id).Scan(&owner, &head, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		err = database.ErrNotFound
	}
	if err != nil {
		return agentuse.Agent{}, err
	}
	if !u.IsAdmin() && u.ID != owner {
		return agentuse.Agent{}, database.ErrNotFound
	}
	if base == "" || base != head {
		return agentuse.Agent{}, skillservice.ErrConflict
	}
	if err = s.validateAgentConfig(ctx, tx, owner, id, &c); err != nil {
		return agentuse.Agent{}, err
	}
	old, err := s.ReadVersion(ctx, tx, head)
	if err != nil {
		return agentuse.Agent{}, err
	}
	if reflect.DeepEqual(c, old.AgentConfig) {
		s.Changes.Publish(notification.Change{Resource: "agents", ID: id}, owner)
		return projection(id, owner, created, updated, old), nil
	}
	now := s.Now().Unix()
	v := agentuse.AgentVersion{ID: uuid.NewString(), AgentID: id, Version: old.Version + 1, AgentConfig: c, CreatedAt: now}
	if err = s.writeAgentVersion(ctx, tx, v); err != nil {
		return agentuse.Agent{}, err
	}
	if _, err = database.Exec(ctx, tx, `UPDATE agents SET current_version_id=?,updated_at=? WHERE id=?`, v.ID, now, id); err != nil {
		return agentuse.Agent{}, err
	}
	if err = tx.Commit().Error; err != nil {
		return agentuse.Agent{}, err
	}
	s.Changes.Publish(notification.Change{Resource: "agents", ID: id}, owner)
	return projection(id, owner, created, now, v), nil
}

func (s *Service) DeleteAgent(ctx context.Context, u agentuse.User, id string) error {
	a, err := s.GetAgent(ctx, u, id)
	if err != nil {
		return err
	}
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = database.Exec(ctx, tx, `UPDATE agents SET deleted_at=?,updated_at=? WHERE id=?`, s.Now().Unix(), s.Now().Unix(), id); err != nil {
		return err
	}
	err = tx.Commit().Error
	if err == nil {
		s.Changes.Publish(notification.Change{Resource: "agents", ID: id}, a.UserID)
		if s.ConversationService != nil {
			s.ConversationService.ParentAgentDeleted(ctx, id)
		}
	}
	return err
}
func (s *Service) GetAgent(ctx context.Context, u agentuse.User, id string) (agentuse.Agent, error) {
	q := database.AgentCols + ` WHERE id=?`
	args := []any{id}
	if !u.IsAdmin() {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	a, err := database.ScanAgent(database.Row(ctx, s.DB, q, args...))
	if err != nil {
		return a, err
	}
	v, err := s.ReadVersion(ctx, s.DB, a.CurrentVersionID)
	a.Skills = v.Skills
	return a, err
}
func (s *Service) ListAgents(ctx context.Context, u agentuse.User) ([]agentuse.Agent, error) {
	q := database.AgentCols
	args := []any{}
	if !u.IsAdmin() {
		q += ` WHERE user_id=?`
		args = append(args, u.ID)
	}
	q += ` ORDER BY name,id`
	rows, err := database.Rows(ctx, s.DB, q, args...)
	if err != nil {
		return nil, err
	}
	out := []agentuse.Agent{}
	for rows.Next() {
		a, e := database.ScanAgent(rows)
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
		v, e := s.ReadVersion(ctx, s.DB, out[i].CurrentVersionID)
		if e != nil {
			return nil, e
		}
		out[i].Skills = v.Skills
	}
	return out, nil
}
