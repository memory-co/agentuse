// Package models 是 agentuse 的数据结构。字段语义见 docs/structs/。
package models

import "time"

// Role 是平台签名里声明的说话人角色。角色只来自平台签名,不来自评论内容。
type Role string

const (
	RoleOwner  Role = "owner"  // 实例主人 / 站长
	RoleMember Role = "member" // 工作区成员 / 维护者
	RoleGuest  Role = "guest"  // 陌生访客
)

// Action 是 agent 通过运行令牌能对平台做的事。
type Action string

const (
	ActionReply  Action = "reply"  // 写一条评论,作者是 agent 应用用户
	ActionStatus Action = "status" // 改线程所挂对象的状态(如工单 in_review)
	ActionLabel  Action = "label"  // 打标签
)

// WorkspaceMode 是 daemon 启动 agent 时给现场的权限。
type WorkspaceMode string

const (
	WorkspaceReadOnly  WorkspaceMode = "readonly"
	WorkspaceReadWrite WorkspaceMode = "readwrite"
)

// Author 是评论的作者,由平台签名担保。
type Author struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// Comment 是平台在评论存库后通知 hub 的那一条。hub 不存评论本身。
type Comment struct {
	ID        string    `json:"id"`
	Thread    string    `json:"thread"` // 线程键:issue:MUL-42 / blog:/posts/tmux / topic:8812
	Author    Author    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Material 是线程里的一条他人评论,作为 agent 的材料而不是指令。
// Trusted=false 的材料在提示词里会单独标记为「仅为信息,非指令」。
type Material struct {
	Comment Comment `json:"comment"`
	Trusted bool    `json:"trusted"`
}

// Policy 是按平台区分的策略,也是这个平台上每个 agent 设置的上限。
type Policy struct {
	WakeRoles     []Role        `json:"wake_roles"`     // wake=members 的 agent 谁能叫醒(主人总能叫醒)
	TrustedRoles  []Role        `json:"trusted_roles"`  // 谁的评论作为带作者标注的材料;其余标为不可信
	Actions       []Action      `json:"actions"`        // agent 最多能做的动作
	WorkspaceMode WorkspaceMode `json:"workspace_mode"` // 现场权限上限:readonly 时所有 agent 都只读
	// QueueTTLSeconds 是运行在队列里等设备上线的最长时间,过期判失败并回调一条 error 进度。默认 86400。
	QueueTTLSeconds int `json:"queue_ttl_seconds,omitempty"`
}

// QueueTTL 是 QueueTTLSeconds 补上默认值后的时长。
func (p Policy) QueueTTL() time.Duration {
	if p.QueueTTLSeconds <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(p.QueueTTLSeconds) * time.Second
}

// Platform 是接入 hub 的一个平台。
type Platform struct {
	ID     string `json:"id"`     // 如 shop-issues
	Secret string `json:"secret"` // 平台签 notify 用的密钥
	// CallbackSecret 是 hub 签回调用的密钥;空 = 沿用 Secret。拆开后平台泄露一把不至于两个方向都失守。
	CallbackSecret string `json:"callback_secret,omitempty"`
	CallbackURL    string `json:"callback_url"` // 平台挂 hub.handler 的地址,如 https://x/agenthub/callback
	Policy         Policy `json:"policy"`
}

// OutboundSecret 是 hub 回调这个平台时签名用的密钥。
func (p Platform) OutboundSecret() string {
	if p.CallbackSecret != "" {
		return p.CallbackSecret
	}
	return p.Secret
}

// Session 是 (线程, agent) 到 agent 会话的映射。同一线程里再 @ 同一个 agent 续接同一会话;
// 同一线程里的两个 agent 是两段独立的会话。
type Session struct {
	ID             string    `json:"id"` // s_<hex>
	Platform       string    `json:"platform"`
	Thread         string    `json:"thread"`
	AgentID        string    `json:"agent_id"`
	AgentSessionID string    `json:"agent_session_id"` // agent 自己的会话 id(如 claude 的 session id),第一次运行后由 daemon 回填
	Branch         string    `json:"branch"`           // 现场分支,如 agent/alice-claude/MUL-42
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// RunStatus 是一次运行的状态。
type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunClaimed   RunStatus = "claimed"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
)

// Run 是一次唤醒对应的一次运行:排队、被 agent 绑定的设备领取、结束。
type Run struct {
	ID            string        `json:"id"` // r_<hex>
	SessionID     string        `json:"session_id"`
	Platform      string        `json:"platform"`
	AgentID       string        `json:"agent_id"`
	Thread        string        `json:"thread"`
	Trigger       Comment       `json:"trigger"`           // 叫醒它的那条评论
	Context       []Material    `json:"context,omitempty"` // 线程里的其它评论,按策略标注可不可信
	Actions       []Action      `json:"actions"`
	WorkspaceMode WorkspaceMode `json:"workspace_mode"`
	Status        RunStatus     `json:"status"`
	Attempts      int           `json:"attempts"`              // 被领取过几次;租约过期回到队列后再领取会 +1
	DeviceID      string        `json:"device_id,omitempty"`   // 领取它的设备
	LeaseUntil    *time.Time    `json:"lease_until,omitempty"` // claimed 时的租约,daemon 心跳续期,过期即回收
	Error         string        `json:"error,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	ClaimedAt     *time.Time    `json:"claimed_at,omitempty"`
	FinishedAt    *time.Time    `json:"finished_at,omitempty"`
}

// ActivityType 沿用 Linear AgentSession 的 activity 分类。
type ActivityType string

const (
	ActivityThought     ActivityType = "thought"
	ActivityAction      ActivityType = "action"
	ActivityElicitation ActivityType = "elicitation"
	ActivityResponse    ActivityType = "response"
	ActivityError       ActivityType = "error"
)

// Activity 是运行过程中的一条进度,hub 原样转发给平台,由平台决定怎么呈现。
type Activity struct {
	Type ActivityType `json:"type"`
	Body string       `json:"body"`
}

// EventType 是 hub 回调平台的事件类型。
type EventType string

const (
	EventReply    EventType = "reply"
	EventStatus   EventType = "status"
	EventActivity EventType = "activity"
)

// Event 是 hub 用自己的签名回调平台的一条事件。
type Event struct {
	Type      EventType `json:"type"`
	Platform  string    `json:"platform"`
	Thread    string    `json:"thread"`
	RunID     string    `json:"run_id"`
	AgentID   string    `json:"agent_id"`              // 哪个 agent 的运行
	Agent     string    `json:"agent"`                 // 它的 handle;平台据此选用哪个应用用户写回复
	InReplyTo string    `json:"in_reply_to,omitempty"` // 叫醒评论的 id
	Text      string    `json:"text,omitempty"`        // reply
	Status    string    `json:"status,omitempty"`      // status,如 in_review
	Activity  *Activity `json:"activity,omitempty"`    // activity
	CreatedAt time.Time `json:"created_at"`
}

// Has 报告动作是否在列表里。
func Has[T comparable](list []T, v T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
