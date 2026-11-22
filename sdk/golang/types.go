package agentuse

import (
	"encoding/json"
)

// Public conversation objects follow docs/server/conversations.md; times are Unix seconds.

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role" enum:"admin,user"`
	CreatedAt int64  `json:"created_at"`
}

type CreateUserRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty" required:"true" minLength:"1" maxLength:"191"`
	Role string `json:"role,omitempty" enum:"admin,user"`
}

type UserToken struct {
	User     User   `json:"user"`
	Token    string `json:"token"`
	LoginURL string `json:"login_url"`
}

type Harness struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

type Agent struct {
	CurrentVersionID string     `json:"current_version_id"`
	Version          int        `json:"version"`
	Model            string     `json:"model,omitempty"`
	Instructions     string     `json:"instructions"`
	Skills           []SkillRef `json:"skills"`
	ID               string     `json:"id"`
	UserID           string     `json:"user_id"`
	Name             string     `json:"name"`
	DeviceID         string     `json:"device_id,omitempty"` // 固定版本的执行设备；设备删除后仍保留历史引用
	Harness          string     `json:"harness"`
	Description      string     `json:"description"`
	CreatedAt        int64      `json:"created_at"`
	UpdatedAt        int64      `json:"updated_at"`
}

type CreateAgentRequest struct {
	Model        string     `json:"model,omitempty"`
	Instructions string     `json:"instructions"`
	Skills       []SkillRef `json:"skills"`
	ID           string     `json:"id,omitempty"` // 全局唯一;空 = 服务生成 ag_xxx
	Name         string     `json:"name"`
	DeviceID     string     `json:"device_id"` // 运行时：自己的某台 device + 它上报过的某个 harness
	Harness      string     `json:"harness"`
	Description  string     `json:"description,omitempty"`
}

type Device struct {
	ClientVersion  string          `json:"client_version,omitempty"`
	ClientUpgrade  *ClientUpgrade  `json:"client_upgrade,omitempty"`
	Capabilities   []string        `json:"capabilities"`
	ID             string          `json:"id"`
	UserID         string          `json:"user_id"`
	Name           string          `json:"name"`
	Status         string          `json:"status" enum:"pending,online,offline"`
	LastSeenAt     *int64          `json:"last_seen_at"`
	CreatedAt      int64           `json:"created_at"`
	Harnesses      []DeviceHarness `json:"harnesses"`
	SetupToken     string          `json:"setup_token,omitempty" doc:"OpenSSH private key body returned by device creation and reset."`
	SetupServerPin string          `json:"setup_server_pin,omitempty" doc:"Server CA fingerprint returned with the registration command."`
	SetupCommand   string          `json:"setup_command,omitempty" doc:"Registration command containing the device key, returned by creation and reset."`
}

type ClientUpgrade struct {
	TargetVersion string `json:"target_version"`
	Status        string `json:"status" enum:"downloading,restarting,succeeded,failed"`
	Error         string `json:"error,omitempty"`
}

type ClientRelease struct {
	Version string `json:"version"`
}

type DeviceHarness struct {
	HarnessRevision string   `json:"harness_revision"`
	Capabilities    []string `json:"capabilities" nullable:"false"`
	DeviceID        string   `json:"device_id"`
	UserID          string   `json:"user_id"`
	Harness         string   `json:"harness"`
	Version         string   `json:"version"`
	Available       bool     `json:"available"`
	Enabled         bool     `json:"enabled"`
	UpdatedAt       int64    `json:"updated_at"`
}

const (
	ConversationModeSingle        = "single"
	ConversationModeCollaborative = "collaborative"
)

type Conversation struct {
	Mode                 string `json:"mode" enum:"single,collaborative"`
	ID                   string `json:"id"`
	UserID               string `json:"user_id"`    // 会话创建者
	CreatedBy            string `json:"created_by"` // 发起人
	ParentConversationID string `json:"parent_conversation_id,omitempty"`
	ParentAgentID        string `json:"parent_agent_id,omitempty"`
	Title                string `json:"title"`
	Status               string `json:"status" enum:"idle,pending,running,waiting,resolved"`
	CreatedAt            int64  `json:"created_at"`
	UpdatedAt            int64  `json:"updated_at"`
}

type Message struct {
	AgentErrors          map[string]MessageError `json:"agent_errors,omitempty"`
	AuthorConversationID string                  `json:"author_conversation_id,omitempty"`
	AuthorRoundSeq       int64                   `json:"author_round_seq,omitempty"`
	AuthorAgentID        string                  `json:"author_agent_id,omitempty"`
	AuthorName           string                  `json:"author_name,omitempty"`
	AuthorRole           string                  `json:"author_role" enum:"user,agent,child_conversation"`
	Attachments          []Attachment            `json:"attachments" nullable:"false"`
	AuthorUserID         string                  `json:"author_user_id,omitempty"`
	ReplyToMessageID     string                  `json:"reply_to_message_id,omitempty"`
	AuthorAgentVersionID string                  `json:"author_agent_version_id,omitempty"`
	Seq                  int64                   `json:"seq"`
	ToAgentIDs           []string                `json:"to_agent_ids" nullable:"false"`
	ToUserIDs            []string                `json:"to_user_ids" nullable:"false"`
	ID                   string                  `json:"id"`
	ConversationID       string                  `json:"conversation_id"`
	Content              string                  `json:"content"`
	CreatedAt            int64                   `json:"created_at"`
}

// MessageError is a negative Client receipt before acceptance, not a run status.
type MessageError struct {
	Error     string `json:"error"`
	Uncertain bool   `json:"uncertain,omitempty"`
}

type ConversationEvent struct {
	AgentVersionID string          `json:"agent_version_id"`
	ID             int64           `json:"id"`
	ConversationID string          `json:"conversation_id"`
	SessionID      string          `json:"session_id"`
	RunID          string          `json:"run_id,omitempty"`
	AgentID        string          `json:"agent_id"`
	DeviceID       string          `json:"device_id"`
	EventSeq       int64           `json:"event_seq"`
	Kind           string          `json:"kind"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAt      int64           `json:"created_at"`
}

// MessageRequest sends text, selected files and an optional reply reference to explicit recipients.
type MessageRequest struct {
	ReplyToMessageID string   `json:"reply_to_message_id,omitempty" doc:"回复同一 conversation 中已有的消息"`
	AttachmentIDs    []string `json:"attachment_ids,omitempty" maxItems:"64" doc:"本条消息选定的同会话附件 ID；省略或空数组表示没有附件"`
	ToAgentIDs       []string `json:"to_agent_ids,omitempty" doc:"处理本条消息的 Agent ID；仅这些收件人创建执行关系"`
	ToUserIDs        []string `json:"to_user_ids,omitempty" doc:"已有会话读取权限的用户 ID；不改变会话可见性"`
	Prompt           string   `json:"prompt,omitempty" doc:"消息文本；两组收件人均省略时只保存备注"`
}

type CreateConversationRequest struct {
	Mode                 string   `json:"mode,omitempty" enum:"single,collaborative" doc:"single（默认）自己执行；collaborative 协调子对话。子对话只能为 single"`
	ParentConversationID string   `json:"parent_conversation_id,omitempty" doc:"仅已校验的顶层 Agent 可创建执行子对话；parent_agent_id 由来源自动确定"`
	Title                string   `json:"title,omitempty"`
	ToAgentIDs           []string `json:"to_agent_ids,omitempty" doc:"处理首条消息的 Agent ID；省略为空数组，重复 ID 去重"`
	ToUserIDs            []string `json:"to_user_ids,omitempty" doc:"已有会话读取权限的用户 ID；不触发执行"`
	Prompt               string   `json:"prompt,omitempty" doc:"首条消息文本；省略时创建空会话"`
}

type CreateConversationResponse struct {
	Round        *ConversationRound `json:"round,omitempty"`
	Runs         []Run              `json:"runs" nullable:"false"`
	RunInputs    []RunInput         `json:"run_inputs" nullable:"false"`
	Conversation Conversation       `json:"conversation"`
	Message      *Message           `json:"message,omitempty"`
}

// ConversationRound is a conversation-local execution interval, not a run or task.
type ConversationRound struct {
	ConversationID      string `json:"conversation_id"`
	Seq                 int64  `json:"seq"`
	StartMessageSeq     int64  `json:"start_message_seq"`
	EndMessageSeq       *int64 `json:"end_message_seq,omitempty"`
	CreatedAt           int64  `json:"created_at"`
	FinishedAt          *int64 `json:"finished_at,omitempty"`
	ParentDelivery      string `json:"parent_delivery,omitempty" enum:"pending,delivered,failed"`
	ParentMessageID     string `json:"parent_message_id,omitempty"`
	ParentDeliveryError string `json:"parent_delivery_error,omitempty"`
}

type ConversationDetail struct {
	Rounds       []ConversationRound `json:"rounds" nullable:"false"`
	Runs         []Run               `json:"runs" nullable:"false"`
	RunInputs    []RunInput          `json:"run_inputs" nullable:"false"`
	RunCounts    map[string]int      `json:"run_counts"`
	Conversation Conversation        `json:"conversation"`
	Messages     []Message           `json:"messages" nullable:"false"`
	Questions    []Question          `json:"questions" nullable:"false"`
	Events       []ConversationEvent `json:"events" nullable:"false"`
}

// Question preserves the public harnessctl request snapshot. Use ReplyRunRequest.
type Question struct {
	DeliveryError  string          `json:"delivery_error,omitempty"`
	RequestID      string          `json:"request_id"`
	ConversationID string          `json:"conversation_id"`
	RunID          string          `json:"run_id"`
	State          string          `json:"state" enum:"pending,replied,expired"`
	Payload        json.RawMessage `json:"payload"`
	Answer         json.RawMessage `json:"answer,omitempty"`
	AnsweredBy     string          `json:"answered_by,omitempty"`
	CreatedAt      int64           `json:"created_at"`
}

// Reply contains the saved message. Execution facts arrive through Client accepted events.
type Reply struct {
	Round     *ConversationRound `json:"round,omitempty"`
	Runs      []Run              `json:"runs"`
	RunInputs []RunInput         `json:"run_inputs" nullable:"false"`
	Message   *Message           `json:"message"`
}

// ConversationSource identifies an existing Client execution; Server verifies it.
// It is not a credential and never creates a second run or session identity.
type ConversationSource struct {
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
	SessionID      string `json:"session_id"`
	RunID          string `json:"run_id"`
}

func (u User) IsAdmin() bool { return u.Role == "admin" }

// Public identity, conversation and device states used by the Server and clients.
const (
	RoleAdmin                = "admin"
	RoleUser                 = "user"
	RoleUserMsg              = "user"
	RoleAgentMsg             = "agent"
	RoleChildConversationMsg = "child_conversation"
	DevicePending            = "pending"
	DeviceOnline             = "online"
	DeviceOffline            = "offline"
	ConversationIdle         = "idle"
	ConversationPending      = "pending"
	ConversationRunning      = "running"
	ConversationWaiting      = "waiting"
	ConversationResolved     = "resolved"
)
