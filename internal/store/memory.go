package store

import (
	"context"
	"sync"
	"time"

	"github.com/memory-co/agentuse/internal/models"
)

// Memory 是进程内实现,重启即丢,用于开发和测试。
type Memory struct {
	mu       sync.Mutex
	sessions map[string]models.Session
	byThread map[string]string // platform + "\x00" + thread → session id
	runs     map[string]models.Run
	queue    []string // 排队中的 run id,先进先出
}

func NewMemory() *Memory {
	return &Memory{
		sessions: map[string]models.Session{},
		byThread: map[string]string{},
		runs:     map[string]models.Run{},
	}
}

func threadKey(platform, thread string) string { return platform + "\x00" + thread }

func (m *Memory) SessionByThread(_ context.Context, platform, thread string) (models.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byThread[threadKey(platform, thread)]
	if !ok {
		return models.Session{}, ErrNotFound
	}
	return m.sessions[id], nil
}

func (m *Memory) PutSession(_ context.Context, s models.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	m.byThread[threadKey(s.Platform, s.Thread)] = s.ID
	return nil
}

func (m *Memory) GetSession(_ context.Context, id string) (models.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return models.Session{}, ErrNotFound
	}
	return s, nil
}

func (m *Memory) PutRun(_ context.Context, r models.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.runs[r.ID]; !exists && r.Status == models.RunQueued {
		m.queue = append(m.queue, r.ID)
	}
	m.runs[r.ID] = r
	return nil
}

func (m *Memory) GetRun(_ context.Context, id string) (models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return models.Run{}, ErrNotFound
	}
	return r, nil
}

func (m *Memory) ClaimRun(_ context.Context, daemonID string) (models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for len(m.queue) > 0 {
		id := m.queue[0]
		m.queue = m.queue[1:]
		r := m.runs[id]
		if r.Status != models.RunQueued {
			continue
		}
		now := time.Now().UTC()
		r.Status, r.DaemonID, r.ClaimedAt = models.RunClaimed, daemonID, &now
		m.runs[id] = r
		return r, nil
	}
	return models.Run{}, ErrNotFound
}
