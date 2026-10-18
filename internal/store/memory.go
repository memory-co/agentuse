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
	users    map[string]models.User // platform + "\x00" + id
	agents   map[string]models.Agent
	devices  map[string]models.Device
	codes    map[string]models.ConnectCode // hash → code
	sessions map[string]models.Session
	byThread map[string]string // platform + thread + agent → session id
	runs     map[string]models.Run
	order    []string // run id,按入队顺序
}

func NewMemory() *Memory {
	return &Memory{
		users:    map[string]models.User{},
		agents:   map[string]models.Agent{},
		devices:  map[string]models.Device{},
		codes:    map[string]models.ConnectCode{},
		sessions: map[string]models.Session{},
		byThread: map[string]string{},
		runs:     map[string]models.Run{},
	}
}

func key(parts ...string) string {
	k := ""
	for i, p := range parts {
		if i > 0 {
			k += "\x00"
		}
		k += p
	}
	return k
}

func (m *Memory) Close() error { return nil }

func (m *Memory) PutUser(_ context.Context, u models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.users[key(u.Platform, u.ID)]; ok {
		u.CreatedAt = old.CreatedAt
	}
	m.users[key(u.Platform, u.ID)] = u
	return nil
}

func (m *Memory) GetUser(_ context.Context, platform, id string) (models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[key(platform, id)]
	if !ok {
		return u, ErrNotFound
	}
	return u, nil
}

func (m *Memory) CreateAgent(_ context.Context, a models.Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.agents {
		if x.ID == a.ID || (x.Platform == a.Platform && x.Handle == a.Handle) {
			return ErrConflict
		}
	}
	m.agents[a.ID] = a
	return nil
}

func (m *Memory) PutAgent(_ context.Context, a models.Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[a.ID]; !ok {
		return ErrNotFound
	}
	m.agents[a.ID] = a
	return nil
}

func (m *Memory) GetAgent(_ context.Context, id string) (models.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	if !ok {
		return a, ErrNotFound
	}
	return a, nil
}

func (m *Memory) AgentByHandle(_ context.Context, platform, handle string) (models.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.agents {
		if a.Platform == platform && a.Handle == handle {
			return a, nil
		}
	}
	return models.Agent{}, ErrNotFound
}

func (m *Memory) ListAgents(_ context.Context, f AgentFilter) ([]models.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []models.Agent{}
	for _, a := range m.agents {
		if (f.Platform == "" || a.Platform == f.Platform) &&
			(f.Owner == "" || a.Owner == f.Owner) &&
			(f.DeviceID == "" || a.DeviceID == f.DeviceID) &&
			(f.AgentUserID == "" || a.AgentUserID == f.AgentUserID) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Memory) DeleteAgent(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[id]; !ok {
		return ErrNotFound
	}
	delete(m.agents, id)
	for h, c := range m.codes {
		if c.AgentID == id {
			delete(m.codes, h)
		}
	}
	return nil
}

func (m *Memory) PutDevice(_ context.Context, d models.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[d.ID] = d
	return nil
}

func (m *Memory) GetDevice(_ context.Context, id string) (models.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return d, ErrNotFound
	}
	return d, nil
}

func (m *Memory) PutConnectCode(_ context.Context, c models.ConnectCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, x := range m.codes {
		if x.AgentID == c.AgentID {
			delete(m.codes, h)
		}
	}
	m.codes[c.Hash] = c
	return nil
}

func (m *Memory) TakeConnectCode(_ context.Context, hash string, now time.Time) (models.ConnectCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[hash]
	if !ok {
		return c, ErrNotFound
	}
	delete(m.codes, hash)
	if !now.Before(c.ExpiresAt) {
		return models.ConnectCode{}, ErrNotFound
	}
	return c, nil
}

func (m *Memory) SessionByThread(_ context.Context, platform, thread, agentID string) (models.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byThread[key(platform, thread, agentID)]
	if !ok {
		return models.Session{}, ErrNotFound
	}
	return m.sessions[id], nil
}

func (m *Memory) PutSession(_ context.Context, s models.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	m.byThread[key(s.Platform, s.Thread, s.AgentID)] = s.ID
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
			(f.AgentID == "" || r.AgentID == f.AgentID) &&
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
		if a, ok := m.agents[r.AgentID]; !ok || a.DeviceID == "" || a.DeviceID != c.DeviceID {
			continue
		}
		now, lease := c.Now, c.LeaseUntil
		r.Status, r.DeviceID, r.ClaimedAt, r.LeaseUntil = models.RunClaimed, c.DeviceID, &now, &lease
		r.Attempts++
		m.runs[id] = r
		return r, nil
	}
	return models.Run{}, ErrNotFound
}

// held 找一条仍被 deviceID 第 attempt 次持有的运行。调用方持锁。
func (m *Memory) held(runID, deviceID string, attempt int) (models.Run, error) {
	r, ok := m.runs[runID]
	if !ok {
		return r, ErrNotFound
	}
	if r.Status != models.RunClaimed || r.DeviceID != deviceID || r.Attempts != attempt {
		return r, ErrConflict
	}
	return r, nil
}

func (m *Memory) ExtendLease(_ context.Context, runID, deviceID string, attempt int, until time.Time) (models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.held(runID, deviceID, attempt)
	if err != nil {
		return r, err
	}
	r.LeaseUntil = &until
	m.runs[runID] = r
	return r, nil
}

func (m *Memory) FinishRun(_ context.Context, runID, deviceID string, attempt int, status models.RunStatus, errMsg string, now time.Time) (models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.held(runID, deviceID, attempt)
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

func (m *Memory) FailRuns(_ context.Context, f FailFilter, errMsg string, now time.Time) ([]models.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.Run
	for _, id := range m.order {
		r := m.runs[id]
		if !failMatches(r, f) {
			continue
		}
		fail(&r, errMsg, now)
		m.runs[id] = r
		out = append(out, r)
	}
	return out, nil
}

// reap 把一条租约过期的运行放回队列或判失败。两种存储共用。
func reap(r *models.Run, now time.Time, maxAttempts int) {
	r.LeaseUntil, r.DeviceID, r.ClaimedAt = nil, "", nil
	if r.Attempts >= maxAttempts {
		r.Status, r.Error, r.FinishedAt = models.RunFailed, "lease expired", &now
		return
	}
	r.Status = models.RunQueued
}
