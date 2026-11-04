package store

import (
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

type Agent = agentuse.Agent

// AttachmentAccess is revalidated inside the upload transaction, including credentials.
type AttachmentAccess struct {
	User                    User
	DaemonID, WorkID, Token string
	RunID                   string
}

const (
	DaemonPending = "pending" // 已在页面上添加，还没跑 setup.sh 注册
	DaemonOnline  = "online"
	DaemonOffline = "offline"
)

type Daemon = agentuse.Daemon

type DaemonHarness = agentuse.DaemonHarness

// ReportedHarness 是 daemon 连上时上报的一个本机可用 harness。
type ReportedHarness struct {
	Revision string `json:"revision,omitempty"`
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
}

type Harness = agentuse.Harness

type Question = agentuse.Question

type Run = agentuse.Run

type Delivery = agentuse.Delivery

type MultiReply = agentuse.Reply

type SessionDetail = agentuse.SessionDetail

const (
	SessionIdle    = "idle"
	SessionPending = "pending"
	SessionRunning = "running"
	SessionWaiting = "waiting" // harness 在等用户回答提问，见 docs/v2/designs/question.md
	// SessionResolved:已完成，见 docs/v2/designs/work.md。再发消息就重新打开。
	SessionResolved = "resolved"

	// 工作里的角色，见 docs/v2/designs/work.md
	WorkCoordinator = "coordinator"
	WorkThread      = "thread"

	RoleUserMsg      = "user"
	RoleAssistantMsg = "assistant"
)

type Session = agentuse.Session

type Message = agentuse.Message

type SessionEvent = agentuse.SessionEvent

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type User = agentuse.User

type Work = agentuse.Work

type WorkPatch = agentuse.WorkPatch
