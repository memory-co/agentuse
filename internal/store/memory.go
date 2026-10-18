package store

import (
	"context"
	"sort"
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
	order    []string // run id,按入队顺序
}

func NewMemory() *Memory {
	return &Memory{
		sessions: map[string]models.Session{},
		byThread: map[string]string{},
		runs:     map[string]models.Run{},
	}
}

func threadKey(platform, thread string) string { return platform + "\x00" + thread }

func (m *Memory) Close() error { return nil }

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

func (m *Memory) ListSessions(_ context.Context, platform string, limit int) ([]models.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []models.Session{}
	for _, s := range m.sessions {
		if platform == "" || s.Platform == platform {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if n := limitOr(limit, 100); len(out) > n {
		out = out[:n]
	}
	return out, nil
}

func (m *Memory) CreateRun(_ context.Context, r models.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[r.ID] = r
	m.order = append(m.order, r.ID)
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

func (m *Memory) ListRuns(_ context.Context, f RunFilter) ([]models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []models.Run{}
	n := limitOr(f.Limit, 100)
	for i := len(m.order) - 1; i >= 0 && len(out) < n; i-- {
		r := m.runs[m.order[i]]
		if (f.Platform == "" || r.Platform == f.Platform) &&
			(f.SessionID == "" || r.SessionID == f.SessionID) &&
			(f.Status == "" || r.Status == f.Status) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *Memory) ClaimRun(_ context.Context, c Claim) (models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	busy := map[string]bool{}
	for _, r := range m.runs {
		if r.Status == models.RunClaimed {
			busy[r.SessionID] = true
		}
	}
	for _, id := range m.order {
		r := m.runs[id]
		if r.Status != models.RunQueued || busy[r.SessionID] {
			continue
		}
		now, lease := c.Now, c.LeaseUntil
		r.Status, r.DaemonID, r.ClaimedAt, r.LeaseUntil = models.RunClaimed, c.DaemonID, &now, &lease
		r.Attempts++
		m.runs[id] = r
		return r, nil
	}
	return models.Run{}, ErrNotFound
}

// held 找一条仍被 daemonID 第 attempt 次持有的运行。调用方持锁。
func (m *Memory) held(runID, daemonID string, attempt int) (models.Run, error) {
	r, ok := m.runs[runID]
	if !ok {
		return r, ErrNotFound
	}
	if r.Status != models.RunClaimed || r.DaemonID != daemonID || r.Attempts != attempt {
		return r, ErrConflict
	}
	return r, nil
}

func (m *Memory) ExtendLease(_ context.Context, runID, daemonID string, attempt int, until time.Time) (models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.held(runID, daemonID, attempt)
	if err != nil {
		return r, err
	}
	r.LeaseUntil = &until
	m.runs[runID] = r
	return r, nil
}

func (m *Memory) FinishRun(_ context.Context, runID, daemonID string, attempt int, status models.RunStatus, errMsg string, now time.Time) (models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.held(runID, daemonID, attempt)
	if err != nil {
		return r, err
	}
	r.Status, r.Error, r.FinishedAt, r.LeaseUntil = status, errMsg, &now, nil
	m.runs[runID] = r
	return r, nil
}

func (m *Memory) ReapExpired(_ context.Context, now time.Time, maxAttempts int) ([]models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.Run
	for _, id := range m.order {
		r := m.runs[id]
		if r.Status != models.RunClaimed || r.LeaseUntil == nil || r.LeaseUntil.After(now) {
			continue
		}
		reap(&r, now, maxAttempts)
		m.runs[id] = r
		out = append(out, r)
	}
	return out, nil
}

// reap 把一条租约过期的运行放回队列或判失败。两种存储共用。
func reap(r *models.Run, now time.Time, maxAttempts int) {
	r.LeaseUntil, r.DaemonID, r.ClaimedAt = nil, "", nil
	if r.Attempts >= maxAttempts {
		r.Status, r.Error, r.FinishedAt = models.RunFailed, "lease expired", &now
		return
	}
	r.Status = models.RunQueued
}
