package user

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

var userIDRe = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)

// CreateUser 建用户，返回用户和登录令牌(只此一次能拿到明文)。
// id 由调用方给(接入方自己的 user id);为空时生成一个 UUID。
func (s *Service) CreateUser(ctx context.Context, id, name, role string) (core.User, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return core.User{}, "", errors.New("用户名不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = core.NewUUID()
	} else if !userIDRe.MatchString(id) {
		return core.User{}, "", errors.New("用户 id 只能用字母、数字和 . _ - : @,最长 128 个字符")
	}
	if role != core.RoleAdmin && role != core.RoleUser {
		return core.User{}, "", fmt.Errorf("未知角色 %q", role)
	}
	u := core.User{ID: id, Name: name, Role: role, CreatedAt: s.Unix()}
	token := core.RandomToken("ut_")
	_, err := s.DB.ExecContext(ctx,
		store.CreateUserInsert,
		u.ID, u.Name, u.Role, core.HashSecret(token), u.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "users.name") {
			return core.User{}, "", fmt.Errorf("用户名 %q 已存在", name)
		}
		if strings.Contains(err.Error(), "users.id") {
			return core.User{}, "", fmt.Errorf("用户 id %q 已存在", id)
		}
		return core.User{}, "", err
	}
	s.NotifyResource("users", u.ID, u.ID)
	return u, token, nil
}

// ResetUserToken 给用户换一个新的登录令牌，旧的立即失效。
func (s *Service) ResetUserToken(ctx context.Context, id string) (core.User, string, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return core.User{}, "", err
	}
	token := core.RandomToken("ut_")
	if _, err := s.DB.ExecContext(ctx, store.ResetUserTokenUpdate, core.HashSecret(token), id); err != nil {
		return core.User{}, "", err
	}
	s.NotifyResource("users", u.ID, u.ID)
	return u, token, nil
}

func (s *Service) GetUser(ctx context.Context, id string) (core.User, error) {
	return s.scanUser(s.DB.QueryRowContext(ctx, store.GetUserSelect, id))
}

// FirstAdmin 是最早建的管理员，服务第一次启动自动建的 admin 一般就是它。
func (s *Service) FirstAdmin(ctx context.Context) (core.User, error) {
	return s.scanUser(s.DB.QueryRowContext(ctx,
		store.FirstAdminSelect, core.RoleAdmin))
}

func (s *Service) UserByToken(ctx context.Context, token string) (core.User, error) {
	return s.scanUser(s.DB.QueryRowContext(ctx,
		store.UserByTokenSelect, core.HashSecret(token)))
}

func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, store.CountUsersSelect).Scan(&n)
	return n, err
}

func (s *Service) ListUsers(ctx context.Context) ([]core.User, error) {
	rows, err := s.DB.QueryContext(ctx, store.ListUsersSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.User
	for rows.Next() {
		u, err := s.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) scanUser(row core.Scanner) (core.User, error) { return store.ScanUser(row) }
