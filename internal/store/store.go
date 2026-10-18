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
	// ErrConflict 表示运行不在调用方以为的状态:不是 claimed,不是这个 daemon 领的,或已被回收重领。
	ErrConflict = errors.New("store: run state conflict")
)

// RunFilter 过滤 ListRuns;零值字段不过滤。
type RunFilter struct {
	Platform  string
	SessionID string
	Status    models.RunStatus
	Limit     int // 0 = 默认 100
}

// Claim 是 ClaimRun 的参数。
type Claim struct {
	DaemonID   string
	Now        time.Time
	LeaseUntil time.Time
}

// Store 是 hub 唯一的状态。它不存评论:评论留在平台。
type Store interface {
	// SessionByThread 找线程键对应的会话;没有返回 ErrNotFound。
	SessionByThread(ctx context.Context, platform, thread string) (models.Session, error)
	PutSession(ctx context.Context, s models.Session) error
	GetSession(ctx context.Context, id string) (models.Session, error)
	// ListSessions 按最近更新倒序;platform 为空 = 全部。
	ListSessions(ctx context.Context, platform string, limit int) ([]models.Session, error)

	// CreateRun 入队一条新运行(status 必须是 queued)。
	CreateRun(ctx context.Context, r models.Run) error
	GetRun(ctx context.Context, id string) (models.Run, error)
	// ListRuns 按创建时间倒序。
	ListRuns(ctx context.Context, f RunFilter) ([]models.Run, error)

	// ClaimRun 原子地领取最早排队、且所属会话没有进行中运行的那一条:
	// 标为 claimed、attempts+1、设租约。没有可领的返回 ErrNotFound。
	// 「所属会话没有进行中运行」保证同一线程的运行严格串行,哪怕有多个 daemon。
	ClaimRun(ctx context.Context, c Claim) (models.Run, error)
	// ExtendLease 续租;运行必须仍是这个 daemon 这一次领取的 claimed 运行,否则 ErrConflict。
	ExtendLease(ctx context.Context, runID, daemonID string, attempt int, until time.Time) (models.Run, error)
	// FinishRun 结束运行(succeeded / failed);条件同 ExtendLease。
	FinishRun(ctx context.Context, runID, daemonID string, attempt int, status models.RunStatus, errMsg string, now time.Time) (models.Run, error)
	// ReapExpired 回收租约已过期的 claimed 运行:attempts < maxAttempts 的回到 queued,其余标 failed。
	ReapExpired(ctx context.Context, now time.Time, maxAttempts int) ([]models.Run, error)

	Close() error
}

func limitOr(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}
