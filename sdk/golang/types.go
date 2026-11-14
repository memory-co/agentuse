package agentuse

import "encoding/json"

// Public conversation objects follow docs/server/conversations.md; times are Unix seconds.

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

type CreateUserRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty" required:"true" minLength:"1"`
	Role string `json:"role,omitempty"`
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
	Capabilities []string        `json:"capabilities"`
	ID           string          `json:"id"`
	UserID       string          `json:"user_id"`
	Name         string          `json:"name"`
	Status       string          `json:"status"` // pending / online / offline
	LastSeenAt   *int64          `json:"last_seen_at"`
	CreatedAt    int64           `json:"created_at"`
	Harnesses    []DeviceHarness `json:"harnesses"`
	SetupToken   string          `json:"setup_token,omitempty"`
	SetupCommand string          `json:"setup_command,omitempty"`
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

type Conversation struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`             // 会话创建者
	CreatedBy string `json:"created_by"`          // 发起人
	WorkID    string `json:"work_id,omitempty"`   // 所属工作，建的时候定，之后不变
	WorkRole  string `json:"work_role,omitempty"` // coordinator / thread,不在工作里时为空
	Title     string `json:"title"`
	Status    string `json:"status"` // idle / pending / running / waiting(在等用户回答提问) / resolved(已完成)
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type Message struct {
	AuthorAgentID        string       `json:"author_agent_id,omitempty"`
	AuthorName           string       `json:"author_name,omitempty"`
	AuthorRole           string       `json:"author_role"`
	Attachments          []Attachment `json:"attachments" nullable:"false"`
	AuthorUserID         string       `json:"author_user_id,omitempty"`
	ReplyToMessageID     string       `json:"reply_to_message_id,omitempty"`
	AuthorAgentVersionID string       `json:"author_agent_version_id,omitempty"`
	Seq                  int64        `json:"seq"`
	ToAgentIDs           []string     `json:"to_agent_ids" nullable:"false"`
	ToUserIDs            []string     `json:"to_user_ids" nullable:"false"`
	ID                   string       `json:"id"`
	ConversationID       string       `json:"conversation_id"`
	Content              string       `json:"content"`
	CreatedAt            int64        `json:"created_at"`
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
	ReplyToMessageID string   `json:"reply_to_message_id,omitempty"`
	AttachmentIDs    []string `json:"attachment_ids,omitempty"`
	ToAgentIDs       []string `json:"to_agent_ids,omitempty"`
	ToUserIDs        []string `json:"to_user_ids,omitempty"`
	Prompt           string   `json:"prompt,omitempty"`
}

type CreateConversationRequest struct {
	Title      string   `json:"title,omitempty"`
	ToAgentIDs []string `json:"to_agent_ids,omitempty"`
	ToUserIDs  []string `json:"to_user_ids,omitempty"`
	Prompt     string   `json:"prompt,omitempty"`
}

type CreateConversationResponse struct {
	Runs         []Run        `json:"runs,omitempty"`
	Deliveries   []Delivery   `json:"deliveries,omitempty"`
	Conversation Conversation `json:"conversation"`
	Message      *Message     `json:"message,omitempty"`
}

type ConversationDetail struct {
	Runs         []Run               `json:"runs"`
	Deliveries   []Delivery          `json:"deliveries"`
	RunCounts    map[string]int      `json:"run_counts"`
	Conversation Conversation        `json:"conversation"`
	Messages     []Message           `json:"messages"`
	Questions    []Question          `json:"questions"`
	Events       []ConversationEvent `json:"events"`
}

// Question preserves the public harnessctl request snapshot. Use ReplyRunRequest.
type Question struct {
	DeliveryError  string          `json:"delivery_error,omitempty"`
	RequestID      string          `json:"request_id"`
	ConversationID string          `json:"conversation_id"`
	RunID          string          `json:"run_id"`
	State          string          `json:"state"`
	Payload        json.RawMessage `json:"payload"`
	Answer         json.RawMessage `json:"answer,omitempty"`
	AnsweredBy     string          `json:"answered_by,omitempty"`
	CreatedAt      int64           `json:"created_at"`
}

// Reply 包含共享消息和各目标的执行/投递。
type Reply struct {
	Runs       []Run      `json:"runs"`
	Deliveries []Delivery `json:"deliveries"`
	Message    *Message   `json:"message"`
}

// Work 是工作：协调者会话 + 若干线程会话 + 一份共用说明，见 docs/server/work.md。
type Work struct {
	CoordinatorAgentID string `json:"coordinator_agent_id,omitempty"`
	ID                 string `json:"id"`
	UserID             string `json:"user_id"`
	Name               string `json:"name"`
	Instructions       string `json:"instructions"`
	CoordinatorID      string `json:"coordinator_id"`
	ThreadCount        int    `json:"thread_count"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type CreateWorkRequest struct {
	Name         string `json:"name"`
	AgentID      string `json:"agent_id"` // 协调者用的 agent
	Instructions string `json:"instructions,omitempty"`
}

type CreateWorkResponse struct {
	Work        Work         `json:"work"`
	Coordinator Conversation `json:"coordinator"`
}

// WorkPatch 只改非 nil 的字段。
type WorkPatch struct {
	CoordinatorAgentID *string `json:"coordinator_agent_id,omitempty"`
	Name               *string `json:"name,omitempty"`
	Instructions       *string `json:"instructions,omitempty"`
}

func (u User) IsAdmin() bool { return u.Role == "admin" }
