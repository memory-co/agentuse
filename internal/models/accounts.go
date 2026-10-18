package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"time"
)

// User 是平台里的一个人。hub 不管注册登录,身份由平台签名担保,这里只记 id 和显示名。
type User struct {
	Platform  string    `json:"platform"`
	ID        string    `json:"id"` // 平台自己的用户 id
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WakeMode 是一个 agent 谁能叫醒。
type WakeMode string

const (
	WakeOwner   WakeMode = "owner"   // 只有主人(默认)
	WakeMembers WakeMode = "members" // 主人,以及平台策略 wake_roles 里的人
)

// Agent 是一个人在平台上的一个 agent:评论里 @ 的名字、主人、绑定的设备和目录、权限。
// 一个 agent 只绑一台设备上的一个目录,见 docs/designs/accounts.md §2。
type Agent struct {
	ID          string `json:"id"` // ag_<hex>
	Platform    string `json:"platform"`
	Handle      string `json:"handle"`        // 评论里 @ 的名字,平台内唯一,如 alice-claude
	AgentUserID string `json:"agent_user_id"` // 平台里同名应用用户的 id;它发的评论不唤醒任何 agent
	Owner       string `json:"owner"`         // 主人的 user_id

	DeviceID string `json:"device_id,omitempty"` // 绑定的设备;空 = 还没接入
	Workdir  string `json:"workdir,omitempty"`   // 设备上的工作目录(git 仓库)
	BaseRef  string `json:"base_ref,omitempty"`  // worktree 从哪个分支切
	Driver   string `json:"driver"`              // claude

	Wake          WakeMode      `json:"wake"`
	Actions       []Action      `json:"actions,omitempty"`        // 空 = 平台策略允许的全部
	WorkspaceMode WorkspaceMode `json:"workspace_mode,omitempty"` // 空 = 平台策略的
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

var handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,38}[a-z0-9]$`)

// ValidHandle 报告 handle 是否合法:小写字母、数字、- 和 _,2 到 40 个字符,首尾是字母或数字。
func ValidHandle(h string) bool { return handleRe.MatchString(h) }

// Effective 是这个 agent 实际能做的动作和现场权限:agent 自己的设置,按平台策略截断。
func (a Agent) Effective(p Policy) ([]Action, WorkspaceMode) {
	actions := p.Actions
	if len(a.Actions) > 0 {
		actions = nil
		for _, x := range a.Actions {
			if Has(p.Actions, x) {
				actions = append(actions, x)
			}
		}
	}
	// 只有平台允许读写、agent 也没自降为只读时才读写;平台没配按只读算。
	mode := WorkspaceReadOnly
	if p.WorkspaceMode == WorkspaceReadWrite && a.WorkspaceMode != WorkspaceReadOnly {
		mode = WorkspaceReadWrite
	}
	return actions, mode
}

// Device 是一个人接进来的一台电脑。daemon 用设备密钥连 hub;hub 只存密钥的 sha256。
type Device struct {
	ID        string     `json:"id"` // dv_<hex>
	Platform  string     `json:"platform"`
	Owner     string     `json:"owner"`
	Name      string     `json:"name"` // 主机名,只用于展示
	OS        string     `json:"os,omitempty"`
	KeyHash   string     `json:"key_hash"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ConnectCode 是一次性的接入码:只能把 AgentID 这个 agent 接到兑换它的那台电脑上。
// hub 只存码的 sha256。
type ConnectCode struct {
	Hash      string    `json:"hash"`
	Platform  string    `json:"platform"`
	AgentID   string    `json:"agent_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ConnectCodeTTL 是接入码的有效期。
const ConnectCodeTTL = 15 * time.Minute

// NewSecret 生成带前缀的 128 位随机串,用于接入码(ac_)和设备密钥(dk_)。
func NewSecret(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// HashSecret 是接入码 / 设备密钥落库的形式。
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
