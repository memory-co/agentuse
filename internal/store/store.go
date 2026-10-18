// Package store 是 hub 的会话表与运行队列。接口在这里,介质可换:
// 现在只有内存实现,SQLite 等持久化实现按同一接口加。
package store

import (
	"context"
	"errors"

	"github.com/memory-co/agentuse/internal/models"
)

var ErrNotFound = errors.New("store: not found")

// Store 是 hub 唯一的状态。它不存评论:评论留在平台。
type Store interface {
	// SessionByThread 找线程键对应的会话;没有返回 ErrNotFound。
	SessionByThread(ctx context.Context, platform, thread string) (models.Session, error)
	PutSession(ctx context.Context, s models.Session) error
	GetSession(ctx context.Context, id string) (models.Session, error)

	PutRun(ctx context.Context, r models.Run) error
	GetRun(ctx context.Context, id string) (models.Run, error)
	// ClaimRun 把最早排队的一条运行原子地标为 claimed 并交回;队列空返回 ErrNotFound。
	ClaimRun(ctx context.Context, daemonID string) (models.Run, error)
}
