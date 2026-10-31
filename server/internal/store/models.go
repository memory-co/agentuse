package store

import (
	"encoding/json"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

type Agent = agentuse.Agent

type AgentPatch = agentuse.UpdateAgentRequest

// AttachmentAccess is revalidated inside the upload transaction, including credentials.
type AttachmentAccess struct {
	User                    User
	DaemonID, WorkID, Token string
	RunID, AttemptID        string
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
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

const (
	HarnessBuiltin = "builtin"
	HarnessCustom  = "custom"
)

type Harness = agentuse.Harness

type Question = agentuse.Question

const (
	QuestionPending  = "pending"
	QuestionAnswered = "answered"
	QuestionExpired  = "expired"
)

type Run = agentuse.Run

type Delivery = agentuse.Delivery

type MultiReply = agentuse.Reply

type RunClaim struct {
	Sender                  agentuse.Sender
	WorkID                  string
	Run                     Run
	Input                   json.RawMessage
	Messages                []Message
	TaskMessageIDs          []int64
	Instructions, WorkToken string
}

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

	EventProgress = "progress"
	EventPlan     = "plan"
	EventSteered  = "steered"
	// 提问，见 docs/v2/designs/question.md
	EventQuestion        = "question"
	EventAnswered        = "answered"
	EventQuestionExpired = "question_expired"
	EventDone            = "done"
	EventFailed          = "failed"
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
