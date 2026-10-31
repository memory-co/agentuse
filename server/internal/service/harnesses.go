package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/server/internal/store"
	"regexp"
	"strings"
)

var harnessNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// CreateCustomHarness 注册用户自定义的 harness。
func (s *Service) CreateCustomHarness(ctx context.Context, ownerID, name, displayName, description string) (Harness, error) {
	name = strings.TrimSpace(name)
	if !harnessNameRe.MatchString(name) {
		return Harness{}, errors.New("harness 名字只能用小写字母、数字和 -")
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = name
	}
	a := Harness{Name: name, Kind: HarnessCustom, DisplayName: displayName, Description: description, OwnerID: ownerID, CreatedAt: s.unix()}
	_, err := s.db.ExecContext(ctx,
		store.CreateCustomHarnessInsert,
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
func (s *Service) DeleteCustomHarness(ctx context.Context, u User, name string) error {
	q := store.DeleteCustomHarnessDelete
	args := []any{name}
	if !u.IsAdmin() {
		q += store.DeleteCustomHarnessClause
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

func (s *Service) ListHarnesses(ctx context.Context) ([]Harness, error) {
	rows, err := s.db.QueryContext(ctx,
		store.ListHarnessesSelect)
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
