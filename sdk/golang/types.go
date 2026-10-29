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

type Harness struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

type CreateHarnessRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type Agent struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	DaemonID    string `json:"daemon_id,omitempty"` // 运行时 = DaemonID + Harness;daemon 被删后为空
	Harness     string `json:"harness"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type CreateAgentRequest struct {
	ID          string `json:"id,omitempty"` // 全局唯一;空 = 服务生成 ag_xxx
	Name        string `json:"name"`
	DaemonID    string `json:"daemon_id"` // 运行时：自己的某台 daemon + 它上报过的某个 harness
	Harness     string `json:"harness"`
	Description string `json:"description,omitempty"`
}

// UpdateAgentRequest 里切换运行时要 DaemonID 和 Harness 一起给。
type UpdateAgentRequest struct {
	Name        *string `json:"name,omitempty"`
	DaemonID    *string `json:"daemon_id,omitempty"`
	Harness     *string `json:"harness,omitempty"`
	Description *string `json:"description,omitempty"`
}

type Daemon struct {
	Capabilities []string        `json:"capabilities"`
	ID           string          `json:"id"`
	UserID       string          `json:"user_id"`
	Name         string          `json:"name"`
	Status       string          `json:"status"` // pending / online / offline
	LastSeenAt   *int64          `json:"last_seen_at"`
	CreatedAt    int64           `json:"created_at"`
	Harnesses    []DaemonHarness `json:"harnesses"`
	SetupToken   string          `json:"setup_token,omitempty"`
	SetupCommand string          `json:"setup_command,omitempty"`
}

type DaemonHarness struct {
	DaemonID  string `json:"daemon_id"`
	UserID    string `json:"user_id"`
	Harness   string `json:"harness"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt int64  `json:"updated_at"`
}

type Session struct {
	ID               string `json:"id"`
	UserID           string `json:"user_id"`             // agent 的主人，会话跑在他的 daemon 上
	CreatedBy        string `json:"created_by"`          // 发起人
	AgentID          string `json:"agent_id,omitempty"`  // agent 删除后为空
	WorkID           string `json:"work_id,omitempty"`   // 所属工作，建的时候定，之后不变
	WorkRole         string `json:"work_role,omitempty"` // coordinator / thread,不在工作里时为空
	Harness          string `json:"harness"`             // 建会话时 agent 运行时的 harness,之后不变
	DaemonID         string `json:"daemon_id,omitempty"`
	HarnessSessionID string `json:"harness_session_id,omitempty"`
	Title            string `json:"title"`
	Status           string `json:"status"` // idle / pending / running / waiting(在等用户回答提问) / resolved(已完成)
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

// MessageRequest 是一条用户消息:给 Prompt 时内容是 {"prompt": …};给 Input 时原样交给 harness。
type MessageRequest struct {
	Prompt string `json:"prompt,omitempty"`
	Input  any    `json:"input,omitempty"`
}

type CreateSessionRequest struct {
	DeferStart bool   `json:"defer_start,omitempty"`
	AgentID    string `json:"agent_id"`
	Prompt     string `json:"prompt,omitempty"`
	Input      any    `json:"input,omitempty"`
}

type CreateSessionResponse struct {
	Session Session  `json:"session"`
	Message *Message `json:"message,omitempty"`
}

type SessionDetail struct {
	Session   Session        `json:"session"`
	Messages  []Message      `json:"messages"`
	Steers    []Steer        `json:"steers"`
	Questions []Question     `json:"questions"`
	Events    []SessionEvent `json:"events"`
}

// Steer 是一条插话：会话忙时发的消息。见 docs/v2/designs/steer.md。
type Steer struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	MessageID int64  `json:"message_id"` // 挂在哪条用户消息上
	CreatedBy string `json:"created_by"`
	Prompt    string `json:"prompt"`
	Status    string `json:"status"` // pending / delivered / queued
	CreatedAt int64  `json:"created_at"`
}

// Question 是 harness 向用户的一次提问，见 docs/v2/designs/question.md。用 AnswerQuestion 回答。
type Question struct {
	ID         int64             `json:"id"`
	SessionID  string            `json:"session_id"`
	MessageID  int64             `json:"message_id"`
	ToolUseID  string            `json:"tool_use_id"`
	Questions  []QuestionItem    `json:"questions"`
	Answers    map[string]string `json:"answers,omitempty"`
	AnsweredBy string            `json:"answered_by,omitempty"`
	Status     string            `json:"status"` // pending / answered / expired
	CreatedAt  int64             `json:"created_at"`
}

type QuestionItem struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	MultiSelect bool             `json:"multi_select,omitempty"`
	Options     []QuestionOption `json:"options,omitempty"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Reply 是 SendMessage 的结果：会话 idle 时是 Message,忙时是 Steer。
type Reply struct {
	Message *Message
	Steer   *Steer
}

// Work 是工作：协调者会话 + 若干线程会话 + 一份共用说明，见 docs/v2/apis/works.md。
type Work struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	Name          string `json:"name"`
	Instructions  string `json:"instructions"`
	CoordinatorID string `json:"coordinator_id"`
	ThreadCount   int    `json:"thread_count"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

type CreateWorkRequest struct {
	Name         string `json:"name"`
	AgentID      string `json:"agent_id"` // 协调者用的 agent
	Instructions string `json:"instructions,omitempty"`
}

type CreateWorkResponse struct {
	Work        Work    `json:"work"`
	Coordinator Session `json:"coordinator"`
}

// WorkPatch 只改非 nil 的字段。
type WorkPatch struct {
	Name         *string `json:"name,omitempty"`
	Instructions *string `json:"instructions,omitempty"`
}
