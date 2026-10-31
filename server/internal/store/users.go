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
	RoleAdmin = "admin"
	RoleUser  = "user"
)

var userIDRe = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// CreateUser 建用户，返回用户和登录令牌(只此一次能拿到明文)。
// id 由调用方给(接入方自己的 user id);为空时生成一个 UUID。
func (s *Store) CreateUser(ctx context.Context, id, name, role string) (User, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return User{}, "", errors.New("用户名不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = newUUID()
	} else if !userIDRe.MatchString(id) {
		return User{}, "", errors.New("用户 id 只能用字母、数字和 . _ - : @,最长 128 个字符")
	}
	if role != RoleAdmin && role != RoleUser {
		return User{}, "", fmt.Errorf("未知角色 %q", role)
	}
	u := User{ID: id, Name: name, Role: role, CreatedAt: s.unix()}
	token := RandomToken("ut_")
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, role, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Name, u.Role, HashSecret(token), u.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "users.name") {
			return User{}, "", fmt.Errorf("用户名 %q 已存在", name)
		}
		if strings.Contains(err.Error(), "users.id") {
			return User{}, "", fmt.Errorf("用户 id %q 已存在", id)
		}
		return User{}, "", err
	}
	return u, token, nil
}

// ResetUserToken 给用户换一个新的登录令牌，旧的立即失效。
func (s *Store) ResetUserToken(ctx context.Context, id string) (User, string, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return User{}, "", err
	}
	token := RandomToken("ut_")
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET token_hash = ? WHERE id = ?`, HashSecret(token), id); err != nil {
		return User{}, "", err
	}
	return u, token, nil
}

func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, `SELECT id, name, role, created_at FROM users WHERE id = ?`, id))
}

// FirstAdmin 是最早建的管理员，服务第一次启动自动建的 admin 一般就是它。
func (s *Store) FirstAdmin(ctx context.Context) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, name, role, created_at FROM users WHERE role = ? ORDER BY created_at, id LIMIT 1`, RoleAdmin))
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
