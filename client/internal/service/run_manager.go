package service

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/memory-co/agentuse/client/internal/store"
)

type endpointWorker struct {
	cfg    Config
	run    *endpointConnection
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// RunService owns all endpoint connections. Metadata and execution capacity are
// shared across workers; one connection's failure does not stop other endpoints.
type RunService struct {
	FailOnEndpointError bool
	Path                string
	Store               *store.Ledger
	Sessions            *SessionService
	Settings            *SettingService
	mu                  sync.Mutex
	workers             map[string]*endpointWorker
	probes              []HarnessInfo
	slots               chan struct{}
	Local               *LocalService
	log                 *log.Logger
}

func NewRunService(path string, files *store.Ledger) (*RunService, error) {
	settings, e := NewSettingService(filepath.Join(filepath.Dir(path), "setting.yaml"))
	if e != nil {
		return nil, e
	}
	m := &RunService{Path: path, Store: files, Settings: settings, Sessions: &SessionService{Files: files, Settings: settings}, workers: map[string]*endpointWorker{}, slots: make(chan struct{}, min(8, max(2, runtime.NumCPU()))), log: log.Default()}
	m.Local = NewLocalService(files, m.Sessions, path, m.slots)
	return m, nil
}
func (s *RunService) Snapshot() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	eps := []map[string]any{}
	running := s.Local.ActiveCount()
	eps = append(eps, map[string]any{"endpoint_id": "local", "state": "local", "active_runs": running})
	for id, w := range s.workers {
		w.run.mu.Lock()
		n := len(w.run.running)
		online := w.run.conn != nil
		w.run.mu.Unlock()
		running += n
		state := "offline"
		if online {
			state = "online"
		}
		if w.err != nil {
			state = "error"
		}
		eps = append(eps, map[string]any{"endpoint_id": id, "state": state, "active_runs": n})
	}
	return map[string]any{"endpoints": eps, "running_runs": running}
}
func (s *RunService) Harnesses() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HarnessInfo{}, s.probes...)
}
func (s *RunService) Run(ctx context.Context) error {
	defer s.Local.Close()
	defer func() {
		s.mu.Lock()
		all := []*endpointWorker{}
		for _, w := range s.workers {
			w.cancel()
			all = append(all, w)
		}
		s.mu.Unlock()
		for _, w := range all {
			<-w.done
		}
	}()
	syncWorkers := func() error {
		endpoints, e := (EndpointService{Path: s.Path}).List()
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e = s.Settings.Reload(); e != nil {
			s.log.Printf("settings reload rejected: %v", e)
		}
		settings := s.Settings.Get()
		wanted := map[string]Endpoint{}
		for _, ep := range endpoints {
			wanted[ep.ID()] = ep
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, w := range s.workers {
			ep, exists := wanted[id]
			drain := !exists || !ep.Enabled || ep.DeviceKey != w.cfg.Key || ep.DeviceID != w.cfg.DaemonID
			rootChanged := settings.Session.WorkRoot != s.Store.Root
			w.run.mu.Lock()
			w.run.paused = drain || rootChanged
			n := len(w.run.running)
			w.run.mu.Unlock()
			if drain && n == 0 {
				w.cancel()
				delete(s.workers, id)
			}
		}
		for id, ep := range wanted {
			if !ep.Enabled || settings.Session.WorkRoot != s.Store.Root {
				continue
			}
			if w := s.workers[id]; w != nil {
				if s.FailOnEndpointError && w.err != nil && len(wanted) == 1 {
					return w.err
				}
				continue
			}
			c := Config{ConfigPath: s.Path, Server: ep.ServerURL, DaemonID: ep.DeviceID, Key: ep.DeviceKey, EndpointID: id, Enabled: true, Settings: settings, LocalStore: s.Store, Slots: s.slots, Sessions: s.Sessions}
			worker, e := New(c, nil, s.Store.ForEndpoint(id))
			if e != nil {
				return e
			}
			wctx, cancel := context.WithCancel(ctx)
			w := &endpointWorker{cfg: c, run: worker, cancel: cancel, done: make(chan struct{})}
			s.workers[id] = w
			go func() {
				defer close(w.done)
				err := worker.Run(wctx)
				s.mu.Lock()
				defer s.mu.Unlock()
				if !errors.Is(err, context.Canceled) {
					w.err = err
					s.log.Printf("endpoint %s: %v", id, err)
				}
			}()
		}
		return nil
	}
	if e := syncWorkers(); e != nil {
		return e
	}
	cfg := Config{ConfigPath: s.Path}
	s.mu.Lock()
	s.probes = ListHarnesses(cfg)
	s.mu.Unlock()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if e := syncWorkers(); e != nil {
				if errors.Is(e, errUnauthorized) {
					return e
				}
				s.log.Printf("endpoints reload rejected: %v", e)
			}
		case <-cleanup.C:
			if q := s.Settings.Get().Session.Quota; q != nil {
				if _, e := s.Store.Reclaim(ctx, q.MaxBytes); e != nil {
					s.log.Printf("quota: %v", e)
				}
			}
		}
	}
}
