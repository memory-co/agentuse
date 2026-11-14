package user

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
	"github.com/memory-co/agentuse/server/internal/store"
)

var userIDRe = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)

// CreateUser 建用户，返回用户和登录令牌(只此一次能拿到明文)。
// id 由调用方给(接入方自己的 user id);为空时生成一个 UUID。
func (s *Service) CreateUser(ctx context.Context, id, name, role string) (agentuse.User, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return agentuse.User{}, "", errors.New("用户名不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = store.NewUUID()
	} else if !userIDRe.MatchString(id) {
		return agentuse.User{}, "", errors.New("用户 id 只能用字母、数字和 . _ - : @,最长 128 个字符")
	}
	if role != agentuse.RoleAdmin && role != agentuse.RoleUser {
		return agentuse.User{}, "", fmt.Errorf("未知角色 %q", role)
	}
	u := agentuse.User{ID: id, Name: name, Role: role, CreatedAt: s.Now().Unix()}
	token := store.RandomToken("ut_")
	_, err := s.DB.ExecContext(ctx,
		store.CreateUserInsert,
		u.ID, u.Name, u.Role, store.HashSecret(token), u.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "users.name") {
			return agentuse.User{}, "", fmt.Errorf("用户名 %q 已存在", name)
		}
		if strings.Contains(err.Error(), "users.id") {
			return agentuse.User{}, "", fmt.Errorf("用户 id %q 已存在", id)
		}
		return agentuse.User{}, "", err
	}
	s.Changes.Publish(notification.Change{Resource: "users", ID: u.ID})
	return u, token, nil
}

// ResetUserToken 给用户换一个新的登录令牌，旧的立即失效。
func (s *Service) ResetUserToken(ctx context.Context, id string) (agentuse.User, string, error) {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return agentuse.User{}, "", err
	}
	token := store.RandomToken("ut_")
	if _, err := s.DB.ExecContext(ctx, store.ResetUserTokenUpdate, store.HashSecret(token), id); err != nil {
		return agentuse.User{}, "", err
	}
	s.Changes.Publish(notification.Change{Resource: "users", ID: u.ID})
	return u, token, nil
}

func (s *Service) GetUser(ctx context.Context, id string) (agentuse.User, error) {
	return store.ScanUser(s.DB.QueryRowContext(ctx, store.GetUserSelect, id))
}

// FirstAdmin 是最早建的管理员，服务第一次启动自动建的 admin 一般就是它。
func (s *Service) FirstAdmin(ctx context.Context) (agentuse.User, error) {
	return store.ScanUser(s.DB.QueryRowContext(ctx,
		store.FirstAdminSelect, agentuse.RoleAdmin))
}

func (s *Service) UserByToken(ctx context.Context, token string) (agentuse.User, error) {
	return store.ScanUser(s.DB.QueryRowContext(ctx,
		store.UserByTokenSelect, store.HashSecret(token)))
}

func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, store.CountUsersSelect).Scan(&n)
	return n, err
}

func (s *Service) ListUsers(ctx context.Context) ([]agentuse.User, error) {
	rows, err := s.DB.QueryContext(ctx, store.ListUsersSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentuse.User
	for rows.Next() {
		u, err := store.ScanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
