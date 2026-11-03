package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/session"

	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/internal/protocol"
)

var ErrLocalBusy = errors.New("session execution is busy or no longer active")

type Request struct {
	Harness     string          `json:"harness"`
	Prompt      string          `json:"prompt"`
	Input       json.RawMessage `json:"input,omitempty"`
	agentID     string
	environment []string
	beforeSend  func(session.Session) error
	accepted    func(harness.Acceptance) error
}
type localExecution struct {
	cancel  context.CancelFunc
	in      *harness.Input
	done    chan struct{}
	session session.Session
	result  harness.DriveResult
}

func (s *Service) Close() { s.localMu.Lock(); s.localCancel(); s.localMu.Unlock(); s.localWG.Wait() }
func (s *Service) CreateConversation(ctx context.Context, id, title string) (session.Conversation, error) {
	ep, err := s.Endpoints.GetByID(id)
	if err != nil {
		return session.Conversation{}, err
	}
	return s.Sessions.CreateConversation(ctx, ep, title)
}

// Start returns only after the adapter publishes accepted, using its actual
// run_id. There is no provisional task UUID and no client execution attempt.
func (s *Service) Start(ctx context.Context, id, conversation string, request Request) (Run, error) {
	if err := s.requireLocalEndpoint(id); err != nil {
		return Run{}, err
	}
	return s.startSession(ctx, id, conversation, request, "")
}
func (s *Service) startSession(ctx context.Context, id, conversation string, request Request, target string) (Run, error) {
	if _, err := s.Endpoints.GetByID(id); err != nil {
		return Run{}, err
	}
	if id == "local" {
		if _, err := s.Sessions.LocalConversation(ctx, conversation); err != nil {
			return Run{}, err
		}
	}
	if !fileutil.Segment(request.Harness) {
		return Run{}, fileutil.ErrInvalid
	}
	input := request.Input
	if len(input) == 0 {
		if strings.TrimSpace(request.Prompt) == "" {
			return Run{}, fileutil.ErrInvalid
		}
		input, _ = json.Marshal(protocol.PromptInput{Prompt: request.Prompt})
	}
	if !json.Valid(input) || len(input) > 256<<10 {
		return Run{}, fileutil.ErrInvalid
	}
	adapter, err := s.Harness.Resolve(request.Harness)
	if err != nil {
		return Run{}, err
	}
	s.localMu.Lock()
	if s.localCtx.Err() != nil {
		s.localMu.Unlock()
		return Run{}, fmt.Errorf("client is stopping")
	}
	// A further send to the same active session is automatically steered/queued
	// by the adapter. Neither the UI nor this service chooses that policy.
	for _, active := range s.localRuns {
		if active.session.EndpointID == id && active.session.ConversationID == conversation && active.session.Harness == request.Harness && (request.agentID == "" || active.session.AgentID == request.agentID) && (target == "" || active.session.ID == target) {
			var pi protocol.PromptInput
			text := string(input)
			if json.Unmarshal(input, &pi) == nil && pi.Prompt != "" {
				text = pi.Prompt
			}
			accepted := make(chan harness.Acceptance, 1)
			if request.beforeSend != nil {
				if err := request.beforeSend(active.session); err != nil {
					s.localMu.Unlock()
					return Run{}, err
				}
			}
			ok := active.in.Send(text, func(a harness.Acceptance) {
				if request.accepted != nil {
					if err := request.accepted(a); err != nil {
						active.cancel()
						return
					}
				}
				accepted <- a
			})
			s.localMu.Unlock()
			if !ok {
				select {
				case <-active.done:
					return s.startSession(ctx, id, conversation, request, target)
				case <-ctx.Done():
					return Run{}, ctx.Err()
				}
			}
			return s.awaitAccepted(ctx, active, accepted, nil)
		}
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.localMu.Unlock()
		return Run{}, ErrLocalBusy
	}
	agentID := request.agentID
	if agentID == "" {
		agentID = "local-" + adapter.Name
	}
	var metadata session.Session
	var cwd string
	if target == "" {
		metadata, cwd, err = s.Sessions.Acquire(ctx, id, conversation, agentID, adapter.Name, adapter.Revision)
	} else {
		metadata, cwd, err = s.Sessions.AcquireExistingSession(ctx, id, conversation, target, adapter.Revision)
	}
	if err != nil {
		<-s.slots
		s.localMu.Unlock()
		return Run{}, err
	}
	if request.beforeSend != nil {
		if err = request.beforeSend(metadata); err != nil {
			<-s.slots
			s.localMu.Unlock()
			_ = s.Sessions.Release(metadata, true)
			return Run{}, err
		}
	}
	adapter.EventsPath = harness.EventsPath(cwd, metadata.ID)
	adapter.Env = []string{"AGENTUSE_SERVER=", "AGENTUSE_TOKEN=", "AGENTUSE_WORK=", "AGENTUSE_CONFIG=", "AGENTUSE_RUN=", "AGENTUSE_ATTEMPT=", "AGENTUSE_ENDPOINT=" + id, "AGENTUSE_CONVERSATION=" + conversation, "AGENTUSE_SESSION=" + metadata.ID, "AGENTUSE_AGENT=" + metadata.AgentID, "AGENTUSE_RESOURCES="}
	if len(request.environment) > 0 {
		adapter.Env = request.environment
	}
	execCtx, cancel := context.WithCancel(s.localCtx)
	execution := &localExecution{cancel: cancel, in: harness.NewInput(harness.DefaultAskTimeout), done: make(chan struct{}), session: metadata}
	s.localRuns[metadata.ID] = execution
	s.localWG.Add(1)
	s.localMu.Unlock()
	accepted := make(chan harness.Acceptance, 1)
	finished := make(chan harness.DriveResult, 1)
	go func() {
		defer s.localWG.Done()
		defer close(execution.done)
		defer cancel()
		defer func() { <-s.slots; s.localMu.Lock(); delete(s.localRuns, metadata.ID); s.localMu.Unlock() }()
		first := true
		report := func(event harness.Event) {
			if event.Type != "accepted" {
				return
			}
			if err := s.AcceptNative(context.Background(), metadata, event.RunID); err != nil {
				s.log.Printf("native run index: %v", err)
				cancel()
				return
			}
			if first {
				first = false
				if request.accepted != nil {
					if err := request.accepted(harness.Acceptance{RunID: event.RunID, InputID: event.InputID}); err != nil {
						cancel()
						return
					}
				}
				accepted <- harness.Acceptance{RunID: event.RunID, InputID: event.InputID}
			}
		}
		result := s.Harness.DriveSession(execCtx, adapter, requestText(input), cwd, execution.in, report)
		if !result.CleanupPending {
			_ = s.Sessions.Release(metadata, result.OK || execCtx.Err() == context.Canceled)
		}
		execution.result = result
		finished <- result
	}()
	return s.awaitAccepted(ctx, execution, accepted, finished)
}
func (s *Service) awaitAccepted(ctx context.Context, execution *localExecution, accepted <-chan harness.Acceptance, finished <-chan harness.DriveResult) (Run, error) {
	select {
	case a := <-accepted:
		return s.Detail(ctx, execution.session.EndpointID, execution.session.ConversationID, execution.session.ID, a.RunID)
	case result := <-finished:
		select {
		case a := <-accepted:
			return s.Detail(ctx, execution.session.EndpointID, execution.session.ConversationID, execution.session.ID, a.RunID)
		default:
		}
		return Run{}, fmt.Errorf("harnessctl send failed before acceptance: %s", result.Error)
	case <-execution.done:
		select {
		case a := <-accepted:
			return s.Detail(ctx, execution.session.EndpointID, execution.session.ConversationID, execution.session.ID, a.RunID)
		default:
			return Run{}, ErrLocalBusy
		}
	case <-ctx.Done():
		return Run{}, ctx.Err()
	}
}

// Control mirrors the public harnessctl session commands, not run attempts.
func (s *Service) Control(ctx context.Context, id, conversation, sessionID, action string, payload json.RawMessage) (any, error) {
	if err := s.requireLocalEndpoint(id); err != nil {
		return nil, err
	}
	return s.controlSession(ctx, id, conversation, sessionID, action, payload)
}
func (s *Service) controlSession(ctx context.Context, id, conversation, session, action string, payload json.RawMessage) (any, error) {
	metadata, err := s.Sessions.Session(ctx, id, conversation, session)
	if err != nil {
		return nil, err
	}
	if action == "send" {
		var request Request
		if json.Unmarshal(payload, &request) != nil || strings.TrimSpace(request.Prompt) == "" {
			return nil, fileutil.ErrInvalid
		}
		request.Harness = metadata.Harness
		request.agentID = metadata.AgentID
		return s.startSession(ctx, id, conversation, request, session)
	}
	s.localMu.Lock()
	execution := s.localRuns[session]
	s.localMu.Unlock()
	if execution == nil {
		if action == "cancel" {
			adapter, e := s.Harness.Resolve(metadata.Harness)
			if e != nil {
				return nil, e
			}
			if metadata.HarnessRevision != adapter.Revision {
				return nil, fmt.Errorf("session adapter revision is not active")
			}
			events := harness.EventsPath(sessionWorkspace(s.Sessions.Root, id, conversation), session)
			if e = harness.Cancel(adapter, events); e != nil {
				return nil, e
			}
			return map[string]bool{"ok": true}, nil
		}
		return nil, ErrLocalBusy
	}
	switch action {
	case "cancel":
		execution.cancel()
		select {
		case <-execution.done:
			if execution.result.CleanupPending {
				return nil, fmt.Errorf("cancellation not confirmed: %s", execution.result.Error)
			}
			return map[string]bool{"ok": true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "reply":
		var request struct {
			RequestID string          `json:"request_id"`
			Answer    json.RawMessage `json:"answer"`
		}
		if json.Unmarshal(payload, &request) != nil || request.RequestID == "" || len(request.Answer) == 0 {
			return nil, fileutil.ErrInvalid
		}
		body := string(request.Answer)
		var text string
		if json.Unmarshal(request.Answer, &text) == nil {
			body = text
		}
		done := make(chan error, 1)
		if !execution.in.Reply(request.RequestID, body, func(err error) { done <- err }) {
			return nil, ErrLocalBusy
		}
		select {
		case err := <-done:
			if err != nil {
				return nil, err
			}
			return map[string]bool{"ok": true}, nil
		case <-execution.done:
			return nil, ErrLocalBusy
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	default:
		return nil, fileutil.ErrInvalid
	}
}
func (s *Service) localActiveCount() int {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	return len(s.localRuns)
}
func (s *Service) requireLocalEndpoint(id string) error {
	ep, err := s.Endpoints.GetByID(id)
	if err != nil {
		return err
	}
	if !ep.IsLocal() {
		return fmt.Errorf("platform inputs are received through their server connection")
	}
	return nil
}
func (s *Service) ProbeHarnesses() ([]harness.HarnessInfo, error) { return s.Harness.Probe() }

func sessionWorkspace(root, endpoint, conversation string) string {
	return filepath.Join(root, endpoint, conversation, "workspace")
}
func (s *Service) SessionStatus(ctx context.Context, id, conversation, session string) (any, error) {
	metadata, err := s.Sessions.Session(ctx, id, conversation, session)
	if err != nil {
		return nil, err
	}
	adapter, err := s.Harness.Resolve(metadata.Harness)
	if err != nil {
		return nil, err
	}
	if adapter.Revision != metadata.HarnessRevision {
		return nil, fmt.Errorf("session adapter revision is not active")
	}
	return s.Harness.Status(ctx, adapter, harness.EventsPath(sessionWorkspace(s.Sessions.Root, id, conversation), session))
}

func requestText(input json.RawMessage) string {
	var request struct {
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal(input, &request) == nil && request.Prompt != "" {
		return request.Prompt
	}
	return string(input)
}
