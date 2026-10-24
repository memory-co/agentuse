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

	// LeaseSeconds:任务租约。daemon 每 HeartbeatSeconds 续一次。
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
	TypeHello     = "hello"     // 连上后第一条：本机可用 agent、正在跑的任务
	TypeReady     = "ready"     // 空闲，可以接活
	TypeEvent     = "event"     // 任务进度 / 结果
	TypeHeartbeat = "heartbeat" // 任务续租

	// 服务 → daemon
	TypeWelcome = "welcome" // 回应 hello
	TypeTask    = "task"    // 派一个任务
	TypeError   = "error"
)

// Message 是 WebSocket 上的一条消息，按 Type 取用对应字段。
type Message struct {
	Type string `json:"type"`

	// hello
	Agents  []Agent  `json:"agents,omitempty"`
	Running []string `json:"running,omitempty"`
	Version string   `json:"version,omitempty"`

	// welcome
	DaemonID string   `json:"daemon_id,omitempty"`
	Name     string   `json:"name,omitempty"`
	Ignored  []string `json:"ignored,omitempty"` // 服务上没登记、被忽略的 agent

	// task
	Task *Task `json:"task,omitempty"`

	// event / heartbeat
	TaskID  string          `json:"task_id,omitempty"`
	Kind    string          `json:"kind,omitempty"` // progress / done / failed
	Payload json.RawMessage `json:"payload,omitempty"`

	// error
	Error string `json:"error,omitempty"`
}

type Agent struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type Task struct {
	ID    string          `json:"id"`
	Agent string          `json:"agent"`
	Input json.RawMessage `json:"input"`
}

// TaskInput 是页面 / API 创建任务时 input 的约定格式。自定义 harness 拿到的是 input 原文。
type TaskInput struct {
	Prompt string `json:"prompt"`
	Cwd    string `json:"cwd,omitempty"` // 在 daemon 本机的哪个目录跑，空 = daemon 的工作目录
}
