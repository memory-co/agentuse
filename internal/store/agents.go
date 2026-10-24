package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	AgentBuiltin = "builtin"
	AgentCustom  = "custom"
)

type Agent struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// BuiltinAgents 是服务初始化时写入的内置 agent。
var BuiltinAgents = []Agent{
	{Name: "claude", Kind: AgentBuiltin, DisplayName: "Claude Code", Description: "Anthropic Claude Code CLI(claude -p)"},
	{Name: "codex", Kind: AgentBuiltin, DisplayName: "Codex", Description: "OpenAI Codex CLI(codex exec)"},
}

var agentNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (s *Store) seedBuiltinAgents(ctx context.Context) error {
	for _, a := range BuiltinAgents {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO agents (name, kind, display_name, description, created_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, display_name = excluded.display_name, description = excluded.description`,
			a.Name, a.Kind, a.DisplayName, a.Description, s.unix())
		if err != nil {
			return fmt.Errorf("store: seed agents: %w", err)
		}
	}
	return nil
}

// CreateCustomAgent 注册用户自定义的 harness。
func (s *Store) CreateCustomAgent(ctx context.Context, ownerID, name, displayName, description string) (Agent, error) {
	name = strings.TrimSpace(name)
	if !agentNameRe.MatchString(name) {
		return Agent{}, errors.New("agent 名字只能用小写字母、数字和 -")
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = name
	}
	a := Agent{Name: name, Kind: AgentCustom, DisplayName: displayName, Description: description, OwnerID: ownerID, CreatedAt: s.unix()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (name, kind, display_name, description, owner_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.Name, a.Kind, a.DisplayName, a.Description, a.OwnerID, a.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY") {
			return Agent{}, fmt.Errorf("agent %q 已存在", name)
		}
		return Agent{}, err
	}
	return a, nil
}

// DeleteCustomAgent 删除自定义 agent。非 admin 只能删自己的;内置的不能删;还有任务引用时不能删。
func (s *Store) DeleteCustomAgent(ctx context.Context, u User, name string) error {
	q := `DELETE FROM agents WHERE name = ? AND kind = 'custom'`
	args := []any{name}
	if !u.IsAdmin() {
		q += ` AND owner_id = ?`
		args = append(args, u.ID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return errors.New("还有任务在用这个 agent,不能删除")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, kind, display_name, description, owner_id, created_at FROM agents
		 ORDER BY kind = 'custom', name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		var desc, owner sql.NullString
		if err := rows.Scan(&a.Name, &a.Kind, &a.DisplayName, &desc, &owner, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Description, a.OwnerID = desc.String, owner.String
		out = append(out, a)
	}
	return out, rows.Err()
}
