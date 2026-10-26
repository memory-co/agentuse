// Package protocol 定义 daemon ⇄ 服务之间的 HTTP 注册接口和 WebSocket 消息。
// 说明见 docs/v2/designs/connect.md。
package protocol

import "encoding/json"

const (
	// RegisterPath:daemon 用安装令牌换长期密钥。
	RegisterPath = "/api/daemon/register"
	// WSPath:daemon 的 WebSocket 长连接，Authorization: Bearer <daemon 密钥>。
	WSPath = "/api/daemon/ws"
	// MePath:daemon 用密钥查自己的信息，不建连接，给 agentuse status 自检用。
	MePath = "/api/daemon/me"

	// LeaseSeconds:会话租约。daemon 每 HeartbeatSeconds 续一次。
	LeaseSeconds     = 60
	HeartbeatSeconds = 15
)

type RegisterRequest struct {
	Token    string `json:"token"`
	Hostname string `json:"hostname"`
}

type RegisterResponse struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	Key      string `json:"key"`
}

type MeResponse struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	UserID   string `json:"user_id"`
	Status   string `json:"status"`
}

// 消息类型。
const (
	// daemon → 服务
	TypeHello     = "hello"     // 连上后第一条：本机可用 harness、正在回的消息
	TypeReady     = "ready"     // 空闲，可以接活
	TypeEvent     = "event"     // 进度 / 结果
	TypeHeartbeat = "heartbeat" // 续租

	// 服务 → daemon
	TypeWelcome = "welcome" // 回应 hello
	TypeMessage = "message" // 派一条用户消息
	TypeSteer   = "steer"   // 推一条插话
	TypeError   = "error"
)

// Message 是 WebSocket 上的一条消息，按 Type 取用对应字段。
type Message struct {
	Type string `json:"type"`

	// hello
	Harnesses []Harness `json:"harnesses,omitempty"`
	Running   []int64   `json:"running,omitempty"` // 正在回的消息 id
	Version   string    `json:"version,omitempty"`

	// welcome
	DaemonID string   `json:"daemon_id,omitempty"`
	Name     string   `json:"name,omitempty"`
	Ignored  []string `json:"ignored,omitempty"` // 服务上没登记、被忽略的 harness

	// message
	Message *Dispatch `json:"message,omitempty"`

	// steer
	Steer *Steer `json:"steer,omitempty"`

	// event / heartbeat / error
	MessageID int64           `json:"message_id,omitempty"`
	Kind      string          `json:"kind,omitempty"` // progress / plan / steered / done / failed
	Payload   json.RawMessage `json:"payload,omitempty"`

	// error
	Error string `json:"error,omitempty"`
}

type Harness struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Dispatch 是派给 daemon 的一条用户消息，带上会话的信息。
type Dispatch struct {
	ID        int64           `json:"id"` // 消息 id
	SessionID string          `json:"session_id"`
	Harness   string          `json:"harness"`
	Resume    string          `json:"resume,omitempty"` // harness 自己的会话 id,第一条消息时为空
	Input     json.RawMessage `json:"input"`            // 消息内容
}

// Steer 是推给 daemon 的一条插话，挂在它正在回的消息 MessageID 上。见 docs/v2/designs/steer.md。
type Steer struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	Prompt    string `json:"prompt"`
}

// SteeredPayload 是 steered 回报的内容。
type SteeredPayload struct {
	SteerID int64 `json:"steer_id"`
}

// PromptInput 是页面 / API 发消息时内容的约定格式。自定义 harness 拿到的是原文。
type PromptInput struct {
	Prompt string `json:"prompt"`
}

// HarnessInput 是写进自定义 harness stdin 的内容。
type HarnessInput struct {
	SessionID string          `json:"session_id"`
	Resume    string          `json:"resume,omitempty"`
	Input     json.RawMessage `json:"input"`
}
