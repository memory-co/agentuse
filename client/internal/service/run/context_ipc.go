package run

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/internal/localipc"
)

func (s *Service) contextAddress() (string, error) {
	root, err := filepath.Abs(s.Sessions.Root)
	if err != nil {
		return "", err
	}
	return localipc.Address(root)
}
func (s *Service) ensureContextIPC() (string, error) {
	s.ipcMu.Lock()
	defer s.ipcMu.Unlock()
	if s.ipcClosed {
		return "", context.Canceled
	}
	if s.ipcServer != nil {
		return s.ipcAddress, nil
	}
	address, err := s.contextAddress()
	if err != nil {
		return "", err
	}
	listener, err := localipc.Listen(address)
	if err != nil {
		return "", err
	}
	server := &http.Server{Handler: http.HandlerFunc(s.proxyContext), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	s.ipcServer = server
	s.ipcAddress = address
	s.localWG.Add(1)
	go func() { defer s.localWG.Done(); _ = server.Serve(listener) }()
	return address, nil
}
func (s *Service) closeContextIPC() {
	s.ipcMu.Lock()
	defer s.ipcMu.Unlock()
	s.ipcClosed = true
	if s.ipcServer != nil {
		_ = s.ipcServer.Close()
		s.ipcServer = nil
	}
}

func (s *Service) proxyContext(w http.ResponseWriter, r *http.Request) {
	// These are local routing assertions, never credentials. The daemon resolves
	// the adapter's actual run and Server verifies its complete scope again.
	if r.Header.Get("Authorization") != "" || r.Header.Get("X-Agentuse-User") != "" {
		http.Error(w, "unsupported local credentials", 403)
		return
	}
	endpointID, conversation, sessionID := r.Header.Get("X-Context-Endpoint"), r.Header.Get("X-Context-Conversation"), r.Header.Get("X-Context-Session")
	metadata, err := s.Sessions.Session(r.Context(), endpointID, conversation, sessionID)
	if err != nil {
		http.Error(w, "unknown session", 403)
		return
	}
	s.mu.Lock()
	worker := s.workers[endpointID]
	s.mu.Unlock()
	if worker == nil {
		http.Error(w, "endpoint offline", 503)
		return
	}
	d := worker.run
	path, known := s.contextAdapters.Load(endpointID + "/" + conversation + "/" + sessionID)
	if !known {
		adapter, e := s.Harness.Resolve(metadata.Harness)
		if e != nil {
			http.Error(w, "adapter unavailable", 503)
			return
		}
		path = adapter.Path
	}
	events := filepath.Join(d.sessionDir(metadata), "events.jsonl")
	state, err := (harness.Service{}).Status(r.Context(), harness.Adapter{Path: path.(string)}, events)
	var token, run string
	if err == nil && state.RunID != nil {
		run = *state.RunID
	}
	requested := r.Header.Get("X-Context-Run")
	if requested != "" && requested != run {
		http.Error(w, "requested run is not the active native run", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if run != "" {
		c := &ExecutionContext{RunID: run}
		if err = c.readRunStatus(ctx, events); err != nil || c.Status != "running" && c.Status != "waiting" {
			http.Error(w, "native run ended", 403)
			return
		}
		for deadline := time.Now().Add(3 * time.Second); ; {
			attempt, stop := context.WithTimeout(ctx, time.Second)
			token, err = d.runAccess(attempt, run)
			stop()
			if err == nil || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				http.Error(w, "access unavailable", 403)
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	} else {
		d.mu.Lock()
		key := d.preparing[sessionID]
		d.mu.Unlock()
		token = d.ticket(key)
		// Only metadata can be proxied before an actual accepted run.
		if r.Method != "GET" || r.URL.Path != "/api/conversations/"+conversation || r.URL.RawQuery != "" {
			http.Error(w, "run has not been accepted", 403)
			return
		}
	}
	if token == "" || (run != "" && err != nil) {
		http.Error(w, "execution access unavailable", 403)
		return
	}
	// The context CLI exposes only conversation operations; no arbitrary resource,
	// identity, certificate or administrative proxying.
	if !(strings.HasPrefix(r.URL.Path, "/api/conversations/") || r.URL.Path == "/api/conversations" || r.Method == "GET" && r.URL.Path == "/api/agents") || r.Method != "GET" && r.Method != "POST" && r.Method != "PUT" {
		http.Error(w, "unsupported context operation", 403)
		return
	}
	ep, err := s.Endpoints.GetByID(endpointID)
	if err != nil {
		http.Error(w, "endpoint unavailable", 503)
		return
	}
	upstream, err := http.NewRequestWithContext(ctx, r.Method, strings.TrimRight(ep.ServerURL, "/")+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	for _, header := range []string{"Content-Type", "Accept"} {
		upstream.Header.Set(header, r.Header.Get(header))
	}
	upstream.Header.Set("Authorization", "Bearer "+token)
	if run != "" {
		upstream.Header.Set("X-Agentuse-Conversation", conversation)
		upstream.Header.Set("X-Agentuse-Agent", metadata.AgentID)
		upstream.Header.Set("X-Agentuse-Session", sessionID)
		upstream.Header.Set("X-Agentuse-Run", run)
	}
	response, err := (&http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}).Do(upstream)
	if err != nil {
		http.Error(w, "Server request failed", 502)
		return
	}
	defer response.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Disposition", "ETag"} {
		if value := response.Header.Get(h); value != "" {
			w.Header().Set(h, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

type contextTransport struct {
	transport *http.Transport
	c         *ExecutionContext
}

func (t contextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.URL.Scheme = "http"
	copy.URL.Host = "context"
	copy.Header.Del("Authorization")
	m := t.c.manifest.Session
	copy.Header.Set("X-Context-Endpoint", m.EndpointID)
	copy.Header.Set("X-Context-Conversation", m.ConversationID)
	copy.Header.Set("X-Context-Session", m.ID)
	copy.Header.Set("X-Context-Run", t.c.RunID)
	return t.transport.RoundTrip(copy)
}
func (c *ExecutionContext) ipcHTTPClient() (*http.Client, error) {
	if c.manifest.IPCAddress == "" {
		return nil, fmt.Errorf("context daemon unavailable; restart this session with the current Client")
	}
	address := c.manifest.IPCAddress
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return localipc.Dial(ctx, address) }, DisableKeepAlives: true}
	return &http.Client{Transport: contextTransport{transport: transport, c: c}, Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
