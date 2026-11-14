// Package agentuse 是 agentuse HTTP API 的 Go 客户端，给网站后端嵌入 agentuse 用。
// 只依赖标准库。接口见 docs/server/conversations.md,用法见 sdk/golang/README.md。
package agentuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UserHeader 是代为某个用户调用时带的请求头，只有 admin 令牌能用。
const UserHeader = "X-Agentuse-User"

// Client 是 agentuse 客户端，不带状态，可以全局共用一个。Token 是 admin 令牌(服务密钥),只能放在网站后端。
// 除了用户管理，每个方法 ctx 之后的参数 user 是网站的 user id,请求带 X-Agentuse-User
// 按那个用户的身份执行;传 "" 就是 admin 本身。
type Client struct {
	Source     *ConversationSource
	RunID      string
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
	c.runHeaders(req)
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

func (c *Client) DeleteAgent(ctx context.Context, user, agentID string) error {
	return c.do(ctx, user, "DELETE", "/api/agents/"+esc(agentID), nil, nil)
}

// ---- devices ----

func (c *Client) ListDevices(ctx context.Context, user string) ([]Device, error) {
	var ds []Device
	return ds, c.do(ctx, user, "GET", "/api/devices", nil, &ds)
}

// CreateDevice 添加一台 device,返回的 SetupCommand 给用户复制到自己电脑上跑。name 可为空。
func (c *Client) CreateDevice(ctx context.Context, user, name string) (*Device, error) {
	var d Device
	return &d, c.do(ctx, user, "POST", "/api/devices", map[string]string{"name": name}, &d)
}

func (c *Client) ResetDevice(ctx context.Context, user, deviceID string) (*Device, error) {
	var d Device
	return &d, c.do(ctx, user, "POST", "/api/devices/"+esc(deviceID)+"/reset", nil, &d)
}

func (c *Client) SetDeviceHarness(ctx context.Context, user, deviceID, harness string, enabled bool) (*Device, error) {
	var d Device
	return &d, c.do(ctx, user, "PUT", "/api/devices/"+esc(deviceID)+"/harnesses/"+esc(harness), map[string]bool{"enabled": enabled}, &d)
}

func (c *Client) DeleteDevice(ctx context.Context, user, deviceID string) error {
	return c.do(ctx, user, "DELETE", "/api/devices/"+esc(deviceID), nil, nil)
}

// ---- conversations ----

func (c *Client) ListConversations(ctx context.Context, user string) ([]Conversation, error) {
	var ss []Conversation
	return ss, c.do(ctx, user, "GET", "/api/conversations", nil, &ss)
}

// CreateConversation 新建会话并发出第一条消息。
func (c *Client) CreateConversation(ctx context.Context, user string, req CreateConversationRequest) (*CreateConversationResponse, error) {
	var r CreateConversationResponse
	return &r, c.do(ctx, user, "POST", "/api/conversations", req, &r)
}

// SendMessage atomically saves a message and routes its target deliveries.
func (c *Client) SendMessage(ctx context.Context, user, conversationID string, req MessageRequest) (*Reply, error) {
	var reply Reply
	return &reply, c.do(ctx, user, "POST", "/api/conversations/"+esc(conversationID)+"/messages", req, &reply)
}

func (c *Client) GetConversation(ctx context.Context, user, conversationID string) (*ConversationDetail, error) {
	var d ConversationDetail
	return &d, c.do(ctx, user, "GET", "/api/conversations/"+esc(conversationID), nil, &d)
}

func (c *Client) DeleteConversation(ctx context.Context, user, conversationID string) error {
	return c.do(ctx, user, "DELETE", "/api/conversations/"+esc(conversationID), nil, nil)
}

// ResolveConversation 把 idle 的会话标记为已完成;再发消息就重新打开。
func (c *Client) ResolveConversation(ctx context.Context, user, conversationID string) (*Conversation, error) {
	var s Conversation
	return &s, c.do(ctx, user, "POST", "/api/conversations/"+esc(conversationID)+"/resolve", nil, &s)
}

// ReopenConversation 取消已完成，回到 idle。
func (c *Client) ReopenConversation(ctx context.Context, user, conversationID string) (*Conversation, error) {
	var s Conversation
	return &s, c.do(ctx, user, "POST", "/api/conversations/"+esc(conversationID)+"/reopen", nil, &s)
}

// ListChildren reads the direct children of an existing parent conversation.
func (c *Client) ListChildren(ctx context.Context, user, parent string) ([]Conversation, error) {
	out := []Conversation{}
	err := c.do(ctx, user, "GET", "/api/conversations?parent_conversation_id="+url.QueryEscape(parent), nil, &out)
	return out, err
}
func (c *Client) GetConversationRound(ctx context.Context, user, conversation string, seq int64) (*ConversationDetail, error) {
	var d ConversationDetail
	err := c.do(ctx, user, "GET", "/api/conversations/"+esc(conversation)+"?round="+strconv.FormatInt(seq, 10), nil, &d)
	return &d, err
}
func (c *Client) DeleteConversationTree(ctx context.Context, user, conversation string) error {
	return c.do(ctx, user, "DELETE", "/api/conversations/"+esc(conversation)+"?recursive=true", nil, nil)
}

func (c *Client) WaitConversation(ctx context.Context, user, conversationID string, interval time.Duration, opts ...WaitOptions) (*ConversationDetail, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		d, err := c.GetConversation(ctx, user, conversationID)
		if err != nil {
			return nil, err
		}
		if d.Conversation.Status == "idle" || d.Conversation.Status == "resolved" || (d.Conversation.Status == "waiting" && (len(opts) > 0 && opts[0].ReturnOnWaiting)) {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return d, ctx.Err()
		case <-t.C:
		}
	}
}

// Request uses the same authenticated transport as the typed SDK methods.
func (c *Client) Request(ctx context.Context, user, method, path string, body, out any) error {
	return c.do(ctx, user, method, path, body, out)
}
func (c *Client) runHeaders(r *http.Request) {
	if c.Source != nil {
		r.Header.Set("X-Agentuse-Conversation", c.Source.ConversationID)
		r.Header.Set("X-Agentuse-Agent", c.Source.AgentID)
		r.Header.Set("X-Agentuse-Session", c.Source.SessionID)
		r.Header.Set("X-Agentuse-Run", c.Source.RunID)
	}
	if c.RunID != "" && (c.Source == nil || c.Source.RunID == "") {
		r.Header.Set("X-Agentuse-Run", c.RunID)
	}
}
