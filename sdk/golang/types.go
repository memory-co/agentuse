package agentuse

import "encoding/json"

// 字段和 docs/v2/apis 里的对象一一对应，时间都是 unix 秒。

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

type CreateUserRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
}

type UserToken struct {
	User     User   `json:"user"`
	Token    string `json:"token"`
	LoginURL string `json:"login_url"`
}

type Agent struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

type CreateAgentRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type Daemon struct {
	ID           string        `json:"id"`
	UserID       string        `json:"user_id"`
	Name         string        `json:"name"`
	Status       string        `json:"status"` // pending / online / offline
	LastSeenAt   *int64        `json:"last_seen_at"`
	CreatedAt    int64         `json:"created_at"`
	Agents       []DaemonAgent `json:"agents"`
	SetupToken   string        `json:"setup_token,omitempty"`
	SetupCommand string        `json:"setup_command,omitempty"`
}

type DaemonAgent struct {
	DaemonID  string `json:"daemon_id"`
	UserID    string `json:"user_id"`
	Agent     string `json:"agent"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt int64  `json:"updated_at"`
}

type Session struct {
	ID               string `json:"id"`
	UserID           string `json:"user_id"`
	Agent            string `json:"agent"`
	DaemonID         string `json:"daemon_id,omitempty"`
	Cwd              string `json:"cwd,omitempty"`
	HarnessSessionID string `json:"harness_session_id,omitempty"`
	Title            string `json:"title"`
	Status           string `json:"status"` // idle / pending / running
	LeaseUntil       *int64 `json:"lease_until,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

type Message struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	Role      string          `json:"role"` // user / assistant
	Content   json.RawMessage `json:"content"`
	CreatedAt int64           `json:"created_at"`
}

type SessionEvent struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	MessageID int64           `json:"message_id"`
	DaemonID  string          `json:"daemon_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

// MessageRequest 是一条用户消息:给 Prompt 时内容是 {"prompt": …};给 Input 时原样交给 agent。
type MessageRequest struct {
	Prompt string `json:"prompt,omitempty"`
	Input  any    `json:"input,omitempty"`
}

type CreateSessionRequest struct {
	Agent  string `json:"agent"`
	Prompt string `json:"prompt,omitempty"`
	Input  any    `json:"input,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

type CreateSessionResponse struct {
	Session Session `json:"session"`
	Message Message `json:"message"`
}

type SessionDetail struct {
	Session  Session        `json:"session"`
	Messages []Message      `json:"messages"`
	Events   []SessionEvent `json:"events"`
}
