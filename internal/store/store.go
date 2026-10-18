// Package store 是 hub 的会话表与运行队列。接口在这里,介质可换:
// Memory(进程内,开发和测试用)和 SQLite(默认,持久化)。两者跑同一套行为测试(storetest)。
package store

import (
	"context"
	"errors"
	"time"

	"github.com/memory-co/agentuse/internal/models"
)

var (
	ErrNotFound = errors.New("store: not found")
	// ErrConflict 表示运行不在调用方以为的状态(不是 claimed、不是这台设备领的、已被回收重领),
	// 或新建的 agent 的 handle 已被占用。
	ErrConflict = errors.New("store: conflict")
)

// RunFilter 过滤 ListRuns;零值字段不过滤。
type RunFilter struct {
	Platform  string
	SessionID string
	AgentID   string
	Status    models.RunStatus
	Limit     int // 0 = 默认 100
}

// Claim 是 ClaimRun 的参数。
type Claim struct {
	DeviceID   string // 只领绑定在这台设备上的 agent 的运行
	Now        time.Time
	LeaseUntil time.Time
}

// AgentFilter 过滤 ListAgents;零值字段不过滤。
type AgentFilter struct {
	Platform    string
	Owner       string
	DeviceID    string
	AgentUserID string
}

// FailFilter 选出要直接判失败的运行(FailRuns)。Statuses 必填,只能是 queued / claimed。
type FailFilter struct {
	Platform      string
	AgentID       string
	CreatedBefore time.Time // 零值 = 不限
	Statuses      []models.RunStatus
}

// Store 是 hub 唯一的状态。它不存评论:评论留在平台。
type Store interface {
	// PutUser 新建或更新(显示名)一个用户。
	PutUser(ctx context.Context, u models.User) error
	GetUser(ctx context.Context, platform, id string) (models.User, error)

	// CreateAgent 新建 agent;同一平台 handle 已被占用返回 ErrConflict。
	CreateAgent(ctx context.Context, a models.Agent) error
	// PutAgent 更新已有的 agent(handle 不可改)。
	PutAgent(ctx context.Context, a models.Agent) error
	GetAgent(ctx context.Context, id string) (models.Agent, error)
	AgentByHandle(ctx context.Context, platform, handle string) (models.Agent, error)
	// ListAgents 按创建时间正序。
	ListAgents(ctx context.Context, f AgentFilter) ([]models.Agent, error)
	// DeleteAgent 删 agent 和它的接入码;会话和运行留作记录。
	DeleteAgent(ctx context.Context, id string) error

	PutDevice(ctx context.Context, d models.Device) error
	GetDevice(ctx context.Context, id string) (models.Device, error)

	// PutConnectCode 存一条接入码,同时作废这个 agent 之前的接入码。
	PutConnectCode(ctx context.Context, c models.ConnectCode) error
	// TakeConnectCode 原子地取走并删除一条接入码;不存在或已过期返回 ErrNotFound。
	TakeConnectCode(ctx context.Context, hash string, now time.Time) (models.ConnectCode, error)

	// SessionByThread 找 (线程, agent) 对应的会话;没有返回 ErrNotFound。
	SessionByThread(ctx context.Context, platform, thread, agentID string) (models.Session, error)
	PutSession(ctx context.Context, s models.Session) error
	GetSession(ctx context.Context, id string) (models.Session, error)
	// ListSessions 按最近更新倒序;platform 为空 = 全部。
	ListSessions(ctx context.Context, platform string, limit int) ([]models.Session, error)

	// CreateRun 入队一条新运行(status 必须是 queued)。
	CreateRun(ctx context.Context, r models.Run) error
	GetRun(ctx context.Context, id string) (models.Run, error)
	// ListRuns 按创建时间倒序。
	ListRuns(ctx context.Context, f RunFilter) ([]models.Run, error)

	// ClaimRun 原子地领取最早排队、所属 agent 绑定在来领的设备上、且所属会话没有进行中运行的那一条:
	// 标为 claimed、attempts+1、设租约。没有可领的返回 ErrNotFound。
	// 「所属会话没有进行中运行」保证同一线程同一 agent 的运行严格串行。
	ClaimRun(ctx context.Context, c Claim) (models.Run, error)
	// ExtendLease 续租;运行必须仍是这台设备这一次领取的 claimed 运行,否则 ErrConflict。
	ExtendLease(ctx context.Context, runID, deviceID string, attempt int, until time.Time) (models.Run, error)
	// FinishRun 结束运行(succeeded / failed);条件同 ExtendLease。
	FinishRun(ctx context.Context, runID, deviceID string, attempt int, status models.RunStatus, errMsg string, now time.Time) (models.Run, error)
	// ReapExpired 回收租约已过期的 claimed 运行:attempts < maxAttempts 的回到 queued,其余标 failed。
	ReapExpired(ctx context.Context, now time.Time, maxAttempts int) ([]models.Run, error)
	// FailRuns 把符合条件的运行直接判失败(排队超时、agent 被删),交回被改的那些。
	FailRuns(ctx context.Context, f FailFilter, errMsg string, now time.Time) ([]models.Run, error)

	Close() error
}

func limitOr(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}

// failMatches 报告运行是否符合 FailFilter。两种存储共用。
func failMatches(r models.Run, f FailFilter) bool {
	return models.Has(f.Statuses, r.Status) &&
		(f.Platform == "" || r.Platform == f.Platform) &&
		(f.AgentID == "" || r.AgentID == f.AgentID) &&
		(f.CreatedBefore.IsZero() || r.CreatedAt.Before(f.CreatedBefore))
}

// fail 把一条运行判失败。两种存储共用。
func fail(r *models.Run, errMsg string, now time.Time) {
	r.Status, r.Error, r.FinishedAt, r.LeaseUntil = models.RunFailed, errMsg, &now, nil
}
