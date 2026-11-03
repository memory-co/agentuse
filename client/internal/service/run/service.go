package run

import (
	"context"
	"errors"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/client/internal/service/setting"
)

type endpointWorker struct {
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
	fileMu              sync.Mutex
	Sessions            *session.Service
	Settings            *setting.Service
	mu                  sync.Mutex
	workers             map[string]*endpointWorker
	slots               chan struct{}
	Endpoints           *endpoint.Service
	Harness             *harness.Service
	localMu             sync.Mutex
	localRuns           map[string]*localExecution
	localCtx            context.Context
	localCancel         context.CancelFunc
	localWG             sync.WaitGroup
	log                 *log.Logger
}

type Dependencies struct {
	Endpoints *endpoint.Service
	Settings  *setting.Service
	Sessions  *session.Service
	Harness   *harness.Service
}

func New(d Dependencies) *Service {
	if d.Endpoints == nil {
		d.Endpoints = &endpoint.Service{}
	}
	m := &Service{Path: d.Endpoints.Path, Endpoints: d.Endpoints, Settings: d.Settings, Sessions: d.Sessions, Harness: d.Harness, workers: map[string]*endpointWorker{}, slots: make(chan struct{}, min(8, max(2, runtime.NumCPU()))), log: log.Default()}
	m.localCtx, m.localCancel = context.WithCancel(context.Background())
	m.localRuns = map[string]*localExecution{}
	m.Sessions.Protected = m.pendingConversation
	return m
}
func (s *Service) Snapshot() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	eps := []map[string]any{}
	running := s.localActiveCount()
	localCount := 0
	s.localMu.Lock()
	for _, active := range s.localRuns {
		if active.session.EndpointID == endpoint.LocalID {
			localCount++
		}
	}
	s.localMu.Unlock()
	eps = append(eps, map[string]any{"endpoint_id": endpoint.LocalID, "state": endpoint.LocalID, "active_sessions": localCount})
	for id, w := range s.workers {
		w.run.mu.Lock()
		online := w.run.conn != nil
		w.run.mu.Unlock()
		n := 0
		s.localMu.Lock()
		for _, active := range s.localRuns {
			if active.session.EndpointID == id {
				n++
			}
		}
		s.localMu.Unlock()
		state := "offline"
		if online {
			state = "online"
		}
		if w.err != nil {
			state = "error"
		}
		eps = append(eps, map[string]any{"endpoint_id": id, "state": state, "active_sessions": n})
	}
	return map[string]any{"endpoints": eps, "active_sessions": running}
}
func (s *Service) Harnesses() any { items, _ := s.Harness.Probe(); return items }

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

	endpoints, err := s.Endpoints.List()
	if err != nil {
		return err
	}
	settings := s.Settings.Get()
	failures := make(chan error, len(endpoints))
	s.mu.Lock()
	for _, ep := range endpoints {
		if ep.IsLocal() || !ep.Enabled {
			continue
		}
		id := ep.ID()
		cfg := Config{ConfigPath: s.Path, Server: ep.ServerURL, DaemonID: ep.DeviceID, Key: ep.DeviceKey, EndpointID: id, Enabled: true, Settings: settings, RunService: s, Slots: s.slots, Sessions: s.Sessions, Harness: s.Harness}
		connection, err := newConnection(cfg, nil)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		wctx, cancel := context.WithCancel(ctx)
		worker := &endpointWorker{run: connection, cancel: cancel, done: make(chan struct{})}
		s.workers[id] = worker
		go func() {
			defer close(worker.done)
			err := connection.Run(wctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				s.mu.Lock()
				worker.err = err
				s.mu.Unlock()
				s.log.Printf("endpoint %s: %v", id, err)
				failures <- err
			}
		}()
	}
	workerCount := len(s.workers)
	s.mu.Unlock()
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			if s.FailOnEndpointError && workerCount == 1 {
				return err
			}
		case <-cleanup.C:
			if q := settings.Session.Quota; q != nil {
				if _, err := s.Sessions.ReclaimToLimit(ctx, q.MaxBytes); err != nil {
					s.log.Printf("quota: %v", err)
				}
			}
		}
	}
}
