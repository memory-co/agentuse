// Package sdk 是平台侧的 Go SDK:设置页替用户建 agent,评论存库后 Notify,回调路由挂 Handler。
//
//	hub := sdk.New(os.Getenv("AGENTUSE_URL"), "shop-issues", os.Getenv("AGENTUSE_SECRET"))
//	created, _ := hub.CreateAgent(ctx, sdk.NewAgent{Owner: u.ID, OwnerName: u.Name, Handle: "alice-claude", AgentUserID: app.ID})
//	// 把 created.Connect.Command 展示给 alice,她在自己电脑上跑
//	hub.Notify(ctx, sdk.Comment{ID: c.ID, Thread: "issue:MUL-42", Author: sdk.Author{...}, Text: c.Body})
//	http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error { ... }))
//
// 签名和验签都由 SDK 负责,平台只处理事件。线协议见 docs/apis/platform.md。
package sdk

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

	"github.com/memory-co/agentuse/pkg/signature"
)

// Author 是评论作者。Role 是平台担保的角色:owner / member / guest。
type Author struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Comment 是一条评论。
type Comment struct {
	ID        string    `json:"id"`
	Thread    string    `json:"thread"`
	Author    Author    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	// Context 是同一线程里的其它评论(可选),hub 会按策略把它们标成可信 / 不可信材料。
	Context []Comment `json:"context,omitempty"`
}

// NotifyResult 是 hub 对 Notify 的回答。
type NotifyResult struct {
	Woken  bool          `json:"woken"`  // 至少叫醒了一个 agent
	Reason string        `json:"reason"` // woken / self_comment / no_mention / not_owner / role_denied
	Agents []AgentResult `json:"agents"` // 评论里 @ 到的每个 agent
}

// AgentResult 是一条评论对一个被 @ 的 agent 的结果。
type AgentResult struct {
	Handle    string `json:"handle"`
	AgentID   string `json:"agent_id"`
	Woken     bool   `json:"woken"`
	Reason    string `json:"reason"`
	Online    bool   `json:"online"` // 不在线时运行排着等它上线,可以据此提示用户
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

// Agent 是一个用户在平台上的 agent。
type Agent struct {
	ID            string    `json:"id"`
	Platform      string    `json:"platform"`
	Handle        string    `json:"handle"`
	AgentUserID   string    `json:"agent_user_id"`
	Owner         string    `json:"owner"`
	DeviceID      string    `json:"device_id,omitempty"`
	Workdir       string    `json:"workdir,omitempty"`
	BaseRef       string    `json:"base_ref,omitempty"`
	Driver        string    `json:"driver"`
	Wake          string    `json:"wake"` // owner / members
	Actions       []string  `json:"actions,omitempty"`
	WorkspaceMode string    `json:"workspace_mode,omitempty"`
	Online        bool      `json:"online"`
	Device        *Device   `json:"device,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Connected 报告 agent 是否已经接到一台电脑上。
func (a Agent) Connected() bool { return a.DeviceID != "" }

// Device 是 agent 绑定的电脑。
type Device struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	OS       string     `json:"os,omitempty"`
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

// NewAgent 是替用户建 agent 的参数。Owner、Handle、AgentUserID 必填。
type NewAgent struct {
	Owner         string   `json:"owner"`
	OwnerName     string   `json:"owner_name,omitempty"`
	Handle        string   `json:"handle"`
	AgentUserID   string   `json:"agent_user_id"`  // 平台里同名应用用户的 id,agent 的回复以它的名义写
	Wake          string   `json:"wake,omitempty"` // 默认 owner
	Actions       []string `json:"actions,omitempty"`
	WorkspaceMode string   `json:"workspace_mode,omitempty"`
}

// AgentPatch 是改 agent 的参数;nil 字段不改。
type AgentPatch struct {
	Wake          *string   `json:"wake,omitempty"`
	Actions       *[]string `json:"actions,omitempty"`
	WorkspaceMode *string   `json:"workspace_mode,omitempty"`
	AgentUserID   *string   `json:"agent_user_id,omitempty"`
}

// ConnectInfo 是一条接入码和要展示给用户的命令。只展示给 agent 的主人。
type ConnectInfo struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	Command   string    `json:"command"`
}

// Created 是 CreateAgent 的结果。
type Created struct {
	Agent   Agent       `json:"agent"`
	Connect ConnectInfo `json:"connect"`
}

// Error 是 hub 交回的非 2xx。Code 如 handle_taken / invalid / not_found。
type Error struct {
	Status  int
	Code    string `json:"error"`
	Message string `json:"message,omitempty"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("agentuse: %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("agentuse: %d %s", e.Status, e.Code)
}

// Activity 是一条进度。
type Activity struct {
	Type string `json:"type"` // thought / action / elicitation / response / error
	Body string `json:"body"`
}

// Event 是 hub 回调平台的事件。
type Event struct {
	Type      string    `json:"type"` // reply / status / activity
	Platform  string    `json:"platform"`
	Thread    string    `json:"thread"`
	RunID     string    `json:"run_id"`
	InReplyTo string    `json:"in_reply_to,omitempty"`
	Text      string    `json:"text,omitempty"`
	Status    string    `json:"status,omitempty"`
	Activity  *Activity `json:"activity,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Client 连一个 hub 实例。
type Client struct {
	URL      string
	Platform string
	Secret   string // 签 notify 用,对应 hub.json 里平台的 secret
	// CallbackSecret 是验 hub 回调用的密钥,对应 hub.json 里平台的 callback_secret;空 = 沿用 Secret。
	CallbackSecret string
	HTTP           *http.Client
}

func New(url, platform, secret string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), Platform: platform, Secret: secret, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Notify 在评论存库后调用。
func (c *Client) Notify(ctx context.Context, cm Comment) (NotifyResult, error) {
	var res NotifyResult
	body, err := json.Marshal(cm)
	if err != nil {
		return res, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/platforms/"+c.Platform+"/notify", bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "application/json")
	signature.SetHeaders(req.Header, c.Secret, body, time.Now())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return res, fmt.Errorf("agentuse notify: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return res, json.NewDecoder(resp.Body).Decode(&res)
}

// CreateAgent 替平台上的一个用户建 agent,交回 agent 和第一条接入命令。
func (c *Client) CreateAgent(ctx context.Context, a NewAgent) (Created, error) {
	var out Created
	return out, c.call(ctx, http.MethodPost, c.agents(""), a, &out)
}

// Agents 列 agent(带在线状态);owner 非空时只列这个人的。
func (c *Client) Agents(ctx context.Context, owner string) ([]Agent, error) {
	var out struct {
		Agents []Agent `json:"agents"`
	}
	path := c.agents("")
	if owner != "" {
		path += "?owner=" + url.QueryEscape(owner)
	}
	return out.Agents, c.call(ctx, http.MethodGet, path, nil, &out)
}

// Agent 查一个 agent。
func (c *Client) Agent(ctx context.Context, id string) (Agent, error) {
	var out Agent
	return out, c.call(ctx, http.MethodGet, c.agents(id), nil, &out)
}

// ConnectCode 给 agent 发一条新接入命令(换电脑、上一条过期了),旧的作废。
func (c *Client) ConnectCode(ctx context.Context, id string) (ConnectInfo, error) {
	var out ConnectInfo
	return out, c.call(ctx, http.MethodPost, c.agents(id)+"/connect-code", nil, &out)
}

// UpdateAgent 改 agent 的唤醒规则、动作等。
func (c *Client) UpdateAgent(ctx context.Context, id string, p AgentPatch) (Agent, error) {
	var out Agent
	return out, c.call(ctx, http.MethodPatch, c.agents(id), p, &out)
}

// DeleteAgent 删 agent:排着和在跑的运行判失败。
func (c *Client) DeleteAgent(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, c.agents(id), nil, nil)
}

// RevokeDevice 吊销一台电脑(丢了):它的密钥作废,绑在上面的 agent 变成未接入。
func (c *Client) RevokeDevice(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/v1/platforms/"+url.PathEscape(c.Platform)+"/devices/"+url.PathEscape(id), nil, nil)
}

func (c *Client) agents(id string) string {
	p := "/v1/platforms/" + url.PathEscape(c.Platform) + "/agents"
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p
}

// call 发一条平台管理请求:签的是方法 + 路径 + 请求体(signature.Request)。
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	signature.SetHeaders(req.Header, c.Secret, signature.Request(method, req.URL.RequestURI(), body), time.Now())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		e := &Error{Status: resp.StatusCode}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(raw, e) != nil || e.Code == "" {
			e.Code = string(bytes.TrimSpace(raw))
		}
		return e
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Handler 是回调路由:验 hub 的签名,把事件交给 fn。fn 返回错误时回 500,hub 会把失败交还给 agent。
func (c *Client) Handler(fn func(context.Context, Event) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		secret := c.CallbackSecret
		if secret == "" {
			secret = c.Secret
		}
		if err := signature.Verify(r.Header, secret, body, time.Now()); err != nil {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		var ev Event
		if err := json.Unmarshal(body, &ev); err != nil {
			http.Error(w, "bad event", http.StatusBadRequest)
			return
		}
		if err := fn(r.Context(), ev); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
