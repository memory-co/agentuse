// Package sdk 是平台侧的 Go SDK:评论存库后 Notify,回调路由挂 Handler。
//
//	hub := sdk.New(os.Getenv("AGENTUSE_URL"), "shop-issues", os.Getenv("AGENTUSE_SECRET"))
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
	Woken     bool   `json:"woken"`
	Reason    string `json:"reason"`
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
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
