package user

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
	"github.com/memory-co/agentuse/server/internal/store"
	"gorm.io/gorm"
)

var userIDRe = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)

// CreateUser 建用户，返回用户和登录令牌(只此一次能拿到明文)。
// id 由调用方给(接入方自己的 user id);为空时生成一个 UUID。
func (s *Service) CreateUser(ctx context.Context, id, name, role string) (agentuse.User, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 191 {
		return agentuse.User{}, "", errors.New("用户名须为 1 到 191 个字符")
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
	err := s.DB.WithContext(ctx).Create(&store.User{ID: u.ID, Name: u.Name, Role: u.Role, TokenHash: store.HashSecret(token), CreatedAt: u.CreatedAt}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			var existing int64
			if s.DB.WithContext(ctx).Model(&store.User{}).Where("id=?", id).Count(&existing).Error == nil && existing > 0 {
				return agentuse.User{}, "", fmt.Errorf("用户 id %q 已存在", id)
			}
			return agentuse.User{}, "", fmt.Errorf("用户名 %q 已存在", name)
		}
		return agentuse.User{}, "", err
	}
	s.Changes.Publish(notification.Change{Resource: "users", ID: u.ID})
	return u, token, nil
}

// ResetUserToken 给用户换一个新的登录令牌，旧的立即失效。
func (s *Service) ResetUserToken(ctx context.Context, id string) (agentuse.User, string, error) {
	token := store.RandomToken("ut_")
	u, err := s.SetUserToken(ctx, id, token)
	if err != nil {
		return agentuse.User{}, "", err
	}
	return u, token, nil
}

// SetUserToken stores a chosen login credential; plaintext is never persisted.
func (s *Service) SetUserToken(ctx context.Context, id, token string) (agentuse.User, error) {
	if err := ValidateUserToken(token); err != nil {
		return agentuse.User{}, err
	}
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return agentuse.User{}, err
	}
	if _, err := store.Exec(ctx, s.DB, store.ResetUserTokenUpdate, store.HashSecret(token), id); err != nil {
		return agentuse.User{}, err
	}
	s.Changes.Publish(notification.Change{Resource: "users", ID: u.ID})
	return u, nil
}

func ValidateUserToken(token string) error {
	if token == "" || len(token) > 1024 || !utf8.ValidString(token) || strings.TrimSpace(token) != token || strings.ContainsFunc(token, unicode.IsControl) {
		return errors.New("登录密码需为 1–1024 字节，不能包含首尾空白或控制字符")
	}
	if strings.HasPrefix(token, "dk_") {
		return errors.New("登录密码不能使用设备密钥保留前缀 dk_")
	}
	return nil
}

func (s *Service) GetUser(ctx context.Context, id string) (agentuse.User, error) {
	return store.ScanUser(store.Row(ctx, s.DB, store.GetUserSelect, id))
}

// FirstAdmin 是最早建的管理员，服务第一次启动自动建的 admin 一般就是它。
func (s *Service) FirstAdmin(ctx context.Context) (agentuse.User, error) {
	return store.ScanUser(store.Row(ctx, s.DB,
		store.FirstAdminSelect, agentuse.RoleAdmin))
}

func (s *Service) UserByToken(ctx context.Context, token string) (agentuse.User, error) {
	return store.ScanUser(store.Row(ctx, s.DB,
		store.UserByTokenSelect, store.HashSecret(token)))
}

func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := store.Row(ctx, s.DB, store.CountUsersSelect).Scan(&n)
	return n, err
}

func (s *Service) ListUsers(ctx context.Context) ([]agentuse.User, error) {
	rows, err := store.Rows(ctx, s.DB, store.ListUsersSelect)
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
