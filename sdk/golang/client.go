// Package agentuse 是 agentuse HTTP API 的 Go 客户端，给网站后端嵌入 agentuse 用。
// 只依赖标准库。接口见 docs/v2/apis,用法见 sdk/golang/README.md。
package agentuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// UserHeader 是代为某个用户调用时带的请求头，只有 admin 令牌能用。
const UserHeader = "X-Agentuse-User"

// Client 是 agentuse 客户端。Token 是 admin 令牌(服务密钥),只能放在网站后端;
// User 不为空时每个请求带 X-Agentuse-User,按那个用户的身份调用。
type Client struct {
	BaseURL    string
	Token      string
	User       string
	HTTPClient *http.Client // 为空时用 http.DefaultClient
}

// New 返回一个以 token 身份调用 baseURL 的客户端。
func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token}
}

// AsUser 返回一个代为 userID 调用的新客户端。
func (c *Client) AsUser(userID string) *Client {
	cp := *c
	cp.User = userID
	return &cp
}

// Error 是非 2xx 响应。
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("agentuse: %d %s", e.Status, e.Message) }

// IsStatus 判断 err 是不是状态码为 status 的 *Error。
func IsStatus(err error, status int) bool {
	e, ok := err.(*Error)
	return ok && e.Status == status
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if c.User != "" {
		req.Header.Set(UserHeader, c.User)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &Error{Status: resp.StatusCode, Message: msg}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func esc(s string) string { return url.PathEscape(s) }

// ---- auth / users ----

// Me 返回当前身份(带 User 时就是那个用户)。
func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	return &u, c.do(ctx, "GET", "/api/me", nil, &u)
}

func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var us []User
	return us, c.do(ctx, "GET", "/api/users", nil, &us)
}

func (c *Client) CreateUser(ctx context.Context, req CreateUserRequest) (*UserToken, error) {
	var t UserToken
	return &t, c.do(ctx, "POST", "/api/users", req, &t)
}

func (c *Client) ResetUserToken(ctx context.Context, userID string) (*UserToken, error) {
	var t UserToken
	return &t, c.do(ctx, "POST", "/api/users/"+esc(userID)+"/token", nil, &t)
}

// ---- agents ----

func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	var as []Agent
	return as, c.do(ctx, "GET", "/api/agents", nil, &as)
}

func (c *Client) CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	var a Agent
	return &a, c.do(ctx, "POST", "/api/agents", req, &a)
}

func (c *Client) DeleteAgent(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/api/agents/"+esc(name), nil, nil)
}

// ---- daemons ----

func (c *Client) ListDaemons(ctx context.Context) ([]Daemon, error) {
	var ds []Daemon
	return ds, c.do(ctx, "GET", "/api/daemons", nil, &ds)
}

// CreateDaemon 添加一台 daemon,返回的 SetupCommand 给用户复制到自己电脑上跑。name 可为空。
func (c *Client) CreateDaemon(ctx context.Context, name string) (*Daemon, error) {
	var d Daemon
	return &d, c.do(ctx, "POST", "/api/daemons", map[string]string{"name": name}, &d)
}

func (c *Client) ResetDaemon(ctx context.Context, daemonID string) (*Daemon, error) {
	var d Daemon
	return &d, c.do(ctx, "POST", "/api/daemons/"+esc(daemonID)+"/reset", nil, &d)
}

func (c *Client) SetDaemonAgent(ctx context.Context, daemonID, agent string, enabled bool) (*Daemon, error) {
	var d Daemon
	return &d, c.do(ctx, "PUT", "/api/daemons/"+esc(daemonID)+"/agents/"+esc(agent), map[string]bool{"enabled": enabled}, &d)
}

func (c *Client) DeleteDaemon(ctx context.Context, daemonID string) error {
	return c.do(ctx, "DELETE", "/api/daemons/"+esc(daemonID), nil, nil)
}

// ---- sessions ----

func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	var ss []Session
	return ss, c.do(ctx, "GET", "/api/sessions", nil, &ss)
}

// CreateSession 新建会话并发出第一条消息。
func (c *Client) CreateSession(ctx context.Context, req CreateSessionRequest) (*CreateSessionResponse, error) {
	var r CreateSessionResponse
	return &r, c.do(ctx, "POST", "/api/sessions", req, &r)
}

// SendMessage 回复。会话不是 idle 时返回 409(用 IsStatus(err, 409) 判断)。
func (c *Client) SendMessage(ctx context.Context, sessionID string, req MessageRequest) (*Message, error) {
	var m Message
	return &m, c.do(ctx, "POST", "/api/sessions/"+esc(sessionID)+"/messages", req, &m)
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (*SessionDetail, error) {
	var d SessionDetail
	return &d, c.do(ctx, "GET", "/api/sessions/"+esc(sessionID), nil, &d)
}

func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	return c.do(ctx, "DELETE", "/api/sessions/"+esc(sessionID), nil, nil)
}

// WaitSession 每隔 interval 轮询一次，直到会话回到 idle,返回最后一次 GetSession 的结果。
// 用 ctx 控制超时。
func (c *Client) WaitSession(ctx context.Context, sessionID string, interval time.Duration) (*SessionDetail, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		d, err := c.GetSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if d.Session.Status == "idle" {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return d, ctx.Err()
		case <-t.C:
		}
	}
}
