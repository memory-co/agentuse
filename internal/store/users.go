package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// CreateUser 建用户，返回用户和登录令牌(只此一次能拿到明文)。
func (s *Store) CreateUser(ctx context.Context, name, role string) (User, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return User{}, "", errors.New("用户名不能为空")
	}
	if role != RoleAdmin && role != RoleUser {
		return User{}, "", fmt.Errorf("未知角色 %q", role)
	}
	u := User{ID: newUUID(), Name: name, Role: role, CreatedAt: s.unix()}
	token := RandomToken("ut_")
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, role, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Name, u.Role, HashSecret(token), u.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, "", fmt.Errorf("用户名 %q 已存在", name)
		}
		return User{}, "", err
	}
	return u, token, nil
}

// ResetUserToken 给用户换一个新的登录令牌，旧的立即失效。
func (s *Store) ResetUserToken(ctx context.Context, name string) (string, error) {
	token := RandomToken("ut_")
	res, err := s.db.ExecContext(ctx, `UPDATE users SET token_hash = ? WHERE name = ?`, HashSecret(token), name)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, role, created_at FROM users WHERE token_hash = ?`, HashSecret(token)))
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, role, created_at FROM users ORDER BY created_at, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := s.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func (s *Store) scanUser(row scanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}
