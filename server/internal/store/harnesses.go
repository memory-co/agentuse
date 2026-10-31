package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/memory-co/agentuse/internal/harness"
)

const (
	HarnessBuiltin = "builtin"
	HarnessCustom  = "custom"
)

type Harness struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

var harnessNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// seedBuiltinHarnesses 把 harness/*/harness.yaml 写进 harnesses 表。目录删了的不删行，免得 agent、会话的引用断掉。
func (s *Store) seedBuiltinHarnesses(ctx context.Context) error {
	builtins, err := harness.All()
	if err != nil {
		return fmt.Errorf("store: load harnesses: %w", err)
	}
	for _, sp := range builtins {
		a := Harness{Name: sp.Name, Kind: HarnessBuiltin, DisplayName: sp.DisplayName, Description: sp.Description}
		if a.DisplayName == "" {
			a.DisplayName = a.Name
		}
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO harnesses (name, kind, display_name, description, created_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, display_name = excluded.display_name, description = excluded.description`,
			a.Name, a.Kind, a.DisplayName, a.Description, s.unix())
		if err != nil {
			return fmt.Errorf("store: seed harnesses: %w", err)
		}
	}
	return nil
}

// CreateCustomHarness 注册用户自定义的 harness。
func (s *Store) CreateCustomHarness(ctx context.Context, ownerID, name, displayName, description string) (Harness, error) {
	name = strings.TrimSpace(name)
	if !harnessNameRe.MatchString(name) {
		return Harness{}, errors.New("harness 名字只能用小写字母、数字和 -")
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = name
	}
	a := Harness{Name: name, Kind: HarnessCustom, DisplayName: displayName, Description: description, OwnerID: ownerID, CreatedAt: s.unix()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO harnesses (name, kind, display_name, description, owner_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.Name, a.Kind, a.DisplayName, a.Description, a.OwnerID, a.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY") {
			return Harness{}, fmt.Errorf("harness %q 已存在", name)
		}
		return Harness{}, err
	}
	return a, nil
}

// DeleteCustomHarness 删除自定义 harness。非 admin 只能删自己的;内置的不能删;还有 agent 或会话引用时不能删。
func (s *Store) DeleteCustomHarness(ctx context.Context, u User, name string) error {
	q := `DELETE FROM harnesses WHERE name = ? AND kind = 'custom'`
	args := []any{name}
	if !u.IsAdmin() {
		q += ` AND owner_id = ?`
		args = append(args, u.ID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return errors.New("还有 agent 或会话在用这个 harness,不能删除")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListHarnesses(ctx context.Context) ([]Harness, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, kind, display_name, description, owner_id, created_at FROM harnesses
		 ORDER BY kind = 'custom', name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Harness
	for rows.Next() {
		var a Harness
		var desc, owner sql.NullString
		if err := rows.Scan(&a.Name, &a.Kind, &a.DisplayName, &desc, &owner, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Description, a.OwnerID = desc.String, owner.String
		out = append(out, a)
	}
	return out, rows.Err()
}
