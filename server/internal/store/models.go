package store

import (
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

type Agent = agentuse.Agent

// AttachmentAccess is revalidated inside the upload transaction, including credentials.
type AttachmentAccess struct {
	User                    User
	DeviceID, WorkID, Token string
	RunID                   string
}

const (
	DevicePending = "pending" // 已在页面上添加，还没跑 setup.sh 注册
	DeviceOnline  = "online"
	DeviceOffline = "offline"
)

type Device = agentuse.Device

type DeviceHarness = agentuse.DeviceHarness

// ReportedHarness 是 device 连上时上报的一个本机可用 harness。
type ReportedHarness struct {
	Capabilities []string `json:"capabilities,omitempty"`
	Revision     string   `json:"revision,omitempty"`
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
}

type Harness = agentuse.Harness

type Question = agentuse.Question

type Run = agentuse.Run

type Delivery = agentuse.Delivery

type Reply = agentuse.Reply

type ConversationDetail = agentuse.ConversationDetail

const (
	ConversationIdle    = "idle"
	ConversationPending = "pending"
	ConversationRunning = "running"
	ConversationWaiting = "waiting" // harness 在等用户回答提问，见 designs/v3/designs/server-run.md
	// ConversationResolved:已完成，见 designs/v3/designs/server-work.md。再发消息就重新打开。
	ConversationResolved = "resolved"

	// 工作里的角色，见 designs/v3/designs/server-work.md
	WorkCoordinator = "coordinator"
	WorkThread      = "thread"

	RoleUserMsg  = "user"
	RoleAgentMsg = "agent"
)

type Conversation = agentuse.Conversation

type Message = agentuse.Message

type ConversationEvent = agentuse.ConversationEvent

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type User = agentuse.User

type Work = agentuse.Work

type WorkPatch = agentuse.WorkPatch
