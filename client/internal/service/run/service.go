package run

import (
	"context"
	"errors"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/client/internal/service/setting"
	"github.com/memory-co/agentuse/client/internal/store"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type endpointWorker struct {
	cfg    Config
	run    *endpointConnection
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// Service owns all endpoint connections. Metadata and execution capacity are
// shared across workers; one connection's failure does not stop other endpoints.
type Service struct {
	FailOnEndpointError bool
	Path                string
	Store               *store.Ledger
	Sessions            *session.Service
	Settings            *setting.Service
	mu                  sync.Mutex
	workers             map[string]*endpointWorker
	probes              []harness.HarnessInfo
	slots               chan struct{}
	Endpoints           *endpoint.Service
	Harness             *harness.Service
	localMu             sync.Mutex
	localRuns           map[string]*localExecution
	localCtx            context.Context
	localCancel         context.CancelFunc
	sequence            atomic.Int64
	localWG             sync.WaitGroup
	log                 *log.Logger
}

type Dependencies struct {
	Endpoints *endpoint.Service
	Settings  *setting.Service
	Sessions  *session.Service
	Harness   *harness.Service
	Store     *store.Ledger
}

func New(d Dependencies) *Service {
	m := &Service{Path: d.Endpoints.Path, Store: d.Store, Endpoints: d.Endpoints, Settings: d.Settings, Sessions: d.Sessions, Harness: d.Harness, workers: map[string]*endpointWorker{}, slots: make(chan struct{}, min(8, max(2, runtime.NumCPU()))), log: log.Default()}
	m.localCtx, m.localCancel = context.WithCancel(context.Background())
	m.localRuns = map[string]*localExecution{}
	return m
}
func (s *Service) Snapshot() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	eps := []map[string]any{}
	running := s.localActiveCount()
	eps = append(eps, map[string]any{"endpoint_id": endpoint.LocalID, "state": endpoint.LocalID, "active_runs": running})
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
func (s *Service) Harnesses() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]harness.HarnessInfo{}, s.probes...)
}
func (s *Service) Run(ctx context.Context) error {
	defer s.Close()
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
		endpoints, e := s.Endpoints.List()
		if e != nil {
			return e
		}
		if e = s.Settings.Reload(); e != nil {
			s.log.Printf("settings reload rejected: %v", e)
		}
		settings := s.Settings.Get()
		wanted := map[string]endpoint.Endpoint{}
		for _, ep := range endpoints {
			if !ep.IsLocal() {
				wanted[ep.ID()] = ep
			}
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
			c := Config{ConfigPath: s.Path, Server: ep.ServerURL, DaemonID: ep.DeviceID, Key: ep.DeviceKey, EndpointID: id, Enabled: true, Settings: settings, LocalStore: s.Store, Slots: s.slots, Sessions: s.Sessions, Harness: s.Harness}
			worker, e := newConnection(c, nil, s.Store.ForEndpoint(id))
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
	s.probes = harness.ListHarnesses(cfg.HarnessConfig())
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
