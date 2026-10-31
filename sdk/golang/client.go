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

// Client 是 agentuse 客户端，不带状态，可以全局共用一个。Token 是 admin 令牌(服务密钥),只能放在网站后端。
// 除了用户管理，每个方法 ctx 之后的参数 user 是网站的 user id,请求带 X-Agentuse-User
// 按那个用户的身份执行;传 "" 就是 admin 本身。
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client // 为空时用 http.DefaultClient
}

// New 返回一个以 token 身份调用 baseURL 的客户端。
func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token}
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

func (c *Client) do(ctx context.Context, user, method, path string, body, out any, extra ...http.Header) error {
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
	for _, headers := range extra {
		for k, values := range headers {
			req.Header[k] = values
		}
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if user != "" {
		req.Header.Set(UserHeader, user)
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

// Me 返回 user 对应的用户(user 为空时是 admin)。
func (c *Client) Me(ctx context.Context, user string) (*User, error) {
	var u User
	return &u, c.do(ctx, user, "GET", "/api/me", nil, &u)
}

func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var us []User
	return us, c.do(ctx, "", "GET", "/api/users", nil, &us)
}

func (c *Client) CreateUser(ctx context.Context, req CreateUserRequest) (*UserToken, error) {
	var t UserToken
	return &t, c.do(ctx, "", "POST", "/api/users", req, &t)
}

func (c *Client) ResetUserToken(ctx context.Context, userID string) (*UserToken, error) {
	var t UserToken
	return &t, c.do(ctx, "", "POST", "/api/users/"+esc(userID)+"/token", nil, &t)
}

// ---- harnesses ----

func (c *Client) ListHarnesses(ctx context.Context, user string) ([]Harness, error) {
	var as []Harness
	return as, c.do(ctx, user, "GET", "/api/harnesses", nil, &as)
}

func (c *Client) CreateHarness(ctx context.Context, user string, req CreateHarnessRequest) (*Harness, error) {
	var a Harness
	return &a, c.do(ctx, user, "POST", "/api/harnesses", req, &a)
}

func (c *Client) DeleteHarness(ctx context.Context, user, name string) error {
	return c.do(ctx, user, "DELETE", "/api/harnesses/"+esc(name), nil, nil)
}

// ---- agents ----

func (c *Client) ListAgents(ctx context.Context, user string) ([]Agent, error) {
	var as []Agent
	return as, c.do(ctx, user, "GET", "/api/agents", nil, &as)
}

func (c *Client) CreateAgent(ctx context.Context, user string, req CreateAgentRequest) (*Agent, error) {
	var a Agent
	return &a, c.do(ctx, user, "POST", "/api/agents", req, &a)
}

func (c *Client) GetAgent(ctx context.Context, user, agentID string) (*Agent, error) {
	var a Agent
	return &a, c.do(ctx, user, "GET", "/api/agents/"+esc(agentID), nil, &a)
}

// UpdateAgent 改名字、切换运行时(DaemonID + Harness 一起给)、改说明;nil 字段不变。切换运行时只影响之后新建的会话。
func (c *Client) UpdateAgent(ctx context.Context, user, agentID string, req UpdateAgentRequest) (*Agent, error) {
	var a Agent
	return &a, c.do(ctx, user, "PATCH", "/api/agents/"+esc(agentID), req, &a)
}

func (c *Client) DeleteAgent(ctx context.Context, user, agentID string) error {
	return c.do(ctx, user, "DELETE", "/api/agents/"+esc(agentID), nil, nil)
}

// ---- daemons ----

func (c *Client) ListDaemons(ctx context.Context, user string) ([]Daemon, error) {
	var ds []Daemon
	return ds, c.do(ctx, user, "GET", "/api/daemons", nil, &ds)
}

// CreateDaemon 添加一台 daemon,返回的 SetupCommand 给用户复制到自己电脑上跑。name 可为空。
func (c *Client) CreateDaemon(ctx context.Context, user, name string) (*Daemon, error) {
	var d Daemon
	return &d, c.do(ctx, user, "POST", "/api/daemons", map[string]string{"name": name}, &d)
}

func (c *Client) ResetDaemon(ctx context.Context, user, daemonID string) (*Daemon, error) {
	var d Daemon
	return &d, c.do(ctx, user, "POST", "/api/daemons/"+esc(daemonID)+"/reset", nil, &d)
}

func (c *Client) SetDaemonHarness(ctx context.Context, user, daemonID, harness string, enabled bool) (*Daemon, error) {
	var d Daemon
	return &d, c.do(ctx, user, "PUT", "/api/daemons/"+esc(daemonID)+"/harnesses/"+esc(harness), map[string]bool{"enabled": enabled}, &d)
}

func (c *Client) DeleteDaemon(ctx context.Context, user, daemonID string) error {
	return c.do(ctx, user, "DELETE", "/api/daemons/"+esc(daemonID), nil, nil)
}

// ---- sessions ----

func (c *Client) ListSessions(ctx context.Context, user string) ([]Session, error) {
	var ss []Session
	return ss, c.do(ctx, user, "GET", "/api/sessions", nil, &ss)
}

// CreateSession 新建会话并发出第一条消息。
func (c *Client) CreateSession(ctx context.Context, user string, req CreateSessionRequest) (*CreateSessionResponse, error) {
	var r CreateSessionResponse
	return &r, c.do(ctx, user, "POST", "/api/sessions", req, &r)
}

// SendMessage atomically saves a message and routes its target deliveries.
func (c *Client) SendMessage(ctx context.Context, user, sessionID string, req MessageRequest) (*Reply, error) {
	var raw map[string]json.RawMessage
	if err := c.do(ctx, user, "POST", "/api/sessions/"+esc(sessionID)+"/messages", req, &raw, func() http.Header {
		h := http.Header{}
		if req.IdempotencyKey != "" {
			h.Set("Idempotency-Key", req.IdempotencyKey)
		}
		return h
	}()); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(raw)
	var reply Reply
	return &reply, json.Unmarshal(b, &reply)

}

func (c *Client) GetSession(ctx context.Context, user, sessionID string) (*SessionDetail, error) {
	var d SessionDetail
	return &d, c.do(ctx, user, "GET", "/api/sessions/"+esc(sessionID), nil, &d)
}

func (c *Client) DeleteSession(ctx context.Context, user, sessionID string) error {
	return c.do(ctx, user, "DELETE", "/api/sessions/"+esc(sessionID), nil, nil)
}

// ResolveSession 把 idle 的会话标记为已完成;再发消息就重新打开。
func (c *Client) ResolveSession(ctx context.Context, user, sessionID string) (*Session, error) {
	var s Session
	return &s, c.do(ctx, user, "POST", "/api/sessions/"+esc(sessionID)+"/resolve", nil, &s)
}

// ReopenSession 取消已完成，回到 idle。
func (c *Client) ReopenSession(ctx context.Context, user, sessionID string) (*Session, error) {
	var s Session
	return &s, c.do(ctx, user, "POST", "/api/sessions/"+esc(sessionID)+"/reopen", nil, &s)
}

// ---- works ----

func (c *Client) ListWorks(ctx context.Context, user string) ([]Work, error) {
	var ws []Work
	return ws, c.do(ctx, user, "GET", "/api/works", nil, &ws)
}

// CreateWork 建工作，同时建出协调者会话(idle)。给协调者发消息用 SendMessage(CoordinatorID)。
func (c *Client) CreateWork(ctx context.Context, user string, req CreateWorkRequest) (*CreateWorkResponse, error) {
	var r CreateWorkResponse
	return &r, c.do(ctx, user, "POST", "/api/works", req, &r)
}

func (c *Client) GetWork(ctx context.Context, user, workID string) (*Work, error) {
	var w Work
	return &w, c.do(ctx, user, "GET", "/api/works/"+esc(workID), nil, &w)
}

func (c *Client) UpdateWork(ctx context.Context, user, workID string, patch WorkPatch) (*Work, error) {
	var w Work
	return &w, c.do(ctx, user, "PATCH", "/api/works/"+esc(workID), patch, &w)
}

// DeleteWork 连协调者和所有线程一起删。
func (c *Client) DeleteWork(ctx context.Context, user, workID string) error {
	return c.do(ctx, user, "DELETE", "/api/works/"+esc(workID), nil, nil)
}

// ListWorkSessions 列工作的线程(不含协调者)。
func (c *Client) ListWorkSessions(ctx context.Context, user, workID string) ([]Session, error) {
	var ss []Session
	return ss, c.do(ctx, user, "GET", "/api/sessions?work_id="+url.QueryEscape(workID), nil, &ss)
}

func (c *Client) WaitSession(ctx context.Context, user, sessionID string, interval time.Duration, opts ...WaitOptions) (*SessionDetail, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		d, err := c.GetSession(ctx, user, sessionID)
		if err != nil {
			return nil, err
		}
		if d.Session.Status == "idle" || d.Session.Status == "resolved" || (d.Session.Status == "waiting" && (len(opts) > 0 && opts[0].ReturnOnWaiting)) {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return d, ctx.Err()
		case <-t.C:
		}
	}
}
