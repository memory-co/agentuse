package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Agent 是用户起了名字的 agent,背后映射一个 harness;换 harness 时 agent 不变。
type Agent struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Harness     string `json:"harness"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// AgentPatch 是修改 agent 时要改的字段，nil 表示不改。
type AgentPatch struct {
	Name        *string
	Harness     *string
	Description *string
}

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

func agentWriteErr(err error, id, name, harness string) error {
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), "agents.id"):
		return fmt.Errorf("agent id %q 已被占用", id)
	case strings.Contains(err.Error(), "UNIQUE"):
		return fmt.Errorf("已经有叫 %q 的 agent", name)
	case strings.Contains(err.Error(), "FOREIGN KEY"):
		return fmt.Errorf("harness %q 没有登记", harness)
	}
	return err
}

// CreateAgent 给用户建一个 agent。id 由调用方传入、全局唯一，不传时生成 ag_xxx;
// 同一用户下名字不能重复,harness 必须登记过。
func (s *Store) CreateAgent(ctx context.Context, userID, id, name, harness, description string) (Agent, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = randomID("ag_")
	} else if !agentIDRe.MatchString(id) {
		return Agent{}, errors.New("agent id 只能用小写字母、数字、- 和 _,最长 64 个字符")
	}
	name, err := cleanAgentName(name)
	if err != nil {
		return Agent{}, err
	}
	harness = strings.TrimSpace(harness)
	if harness == "" {
		return Agent{}, errors.New("harness 必填")
	}
	now := s.unix()
	a := Agent{ID: id, UserID: userID, Name: name, Harness: harness, Description: description, CreatedAt: now, UpdatedAt: now}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agents (id, user_id, name, harness, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.UserID, a.Name, a.Harness, a.Description, now, now)
	if err != nil {
		return Agent{}, agentWriteErr(err, id, name, harness)
	}
	return a, nil
}

// UpdateAgent 改名字、换 harness 或改说明，只有主人(或 admin)能改。已有会话不受影响，换 harness 只对之后新建的会话生效。
func (s *Store) UpdateAgent(ctx context.Context, u User, id string, p AgentPatch) (Agent, error) {
	a, err := s.GetAgent(ctx, u, id)
	if err != nil {
		return Agent{}, err
	}
	if p.Name != nil {
		if a.Name, err = cleanAgentName(*p.Name); err != nil {
			return Agent{}, err
		}
	}
	if p.Harness != nil {
		if a.Harness = strings.TrimSpace(*p.Harness); a.Harness == "" {
			return Agent{}, errors.New("harness 必填")
		}
	}
	if p.Description != nil {
		a.Description = *p.Description
	}
	a.UpdatedAt = s.unix()
	_, err = s.db.ExecContext(ctx,
		`UPDATE agents SET name = ?, harness = ?, description = ?, updated_at = ? WHERE id = ?`,
		a.Name, a.Harness, a.Description, a.UpdatedAt, a.ID)
	if err != nil {
		return Agent{}, agentWriteErr(err, a.ID, a.Name, a.Harness)
	}
	return a, nil
}

// DeleteAgent 删除 agent。用过它的会话保留，会话的 agent_id 变空。
func (s *Store) DeleteAgent(ctx context.Context, u User, id string) error {
	q := `DELETE FROM agents WHERE id = ?`
	args := []any{id}
	if !u.IsAdmin() {
		q += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const agentCols = `SELECT id, user_id, name, harness, description, created_at, updated_at FROM agents`

func scanAgent(row scanner) (Agent, error) {
	var a Agent
	var desc sql.NullString
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.Harness, &desc, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	a.Description = desc.String
	return a, err
}

func (s *Store) GetAgent(ctx context.Context, u User, id string) (Agent, error) {
	q := agentCols + ` WHERE id = ?`
	args := []any{id}
	if !u.IsAdmin() {
		q += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	return scanAgent(s.db.QueryRowContext(ctx, q, args...))
}

// ListAgents 列 agent,按名字排序;admin 看所有人的。
func (s *Store) ListAgents(ctx context.Context, u User) ([]Agent, error) {
	q := agentCols
	var args []any
	if !u.IsAdmin() {
		q += ` WHERE user_id = ?`
		args = append(args, u.ID)
	}
	q += ` ORDER BY name, id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
