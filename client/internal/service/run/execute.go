package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/protocol"
	"path/filepath"
	"strings"
	"time"
)

var ErrLocalBusy = errors.New("local harness is busy")

type Request struct {
	Harness string          `json:"harness"`
	Prompt  string          `json:"prompt"`
	Input   json.RawMessage `json:"input,omitempty"`
}
type localExecution struct {
	canSteer bool
	canAsk   bool
	cancel   context.CancelFunc
	in       *harness.Input
	done     chan struct{}
	session  string
}

func (s *Service) Close() { s.localMu.Lock(); s.localCancel(); s.localMu.Unlock(); s.localWG.Wait() }
func (s *Service) CreateConversation(ctx context.Context, endpointID, title string) (store.Conversation, error) {
	ep, e := s.Endpoints.GetByID(endpointID)
	if e != nil {
		return store.Conversation{}, e
	}
	return s.Sessions.CreateConversation(ctx, ep, title)
}
func (s *Service) Start(ctx context.Context, endpointID, conversation string, request Request) (store.Run, error) {
	if e := s.requireLocalEndpoint(endpointID); e != nil {
		return store.Run{}, e
	}
	if _, e := s.Store.LocalConversation(ctx, conversation); e != nil {
		return store.Run{}, e
	}
	if !store.Segment(request.Harness) {
		return store.Run{}, store.ErrInvalid
	}
	input := request.Input
	if len(input) == 0 {
		if strings.TrimSpace(request.Prompt) == "" {
			return store.Run{}, store.ErrInvalid
		}
		input, _ = json.Marshal(protocol.PromptInput{Prompt: request.Prompt})
	}
	if !json.Valid(input) || len(input) > 256<<10 {
		return store.Run{}, store.ErrInvalid
	}
	la, e := s.Harness.Resolve(request.Harness)
	if e != nil {
		return store.Run{}, e
	}

	s.localMu.Lock()
	defer s.localMu.Unlock()
	if s.localCtx.Err() != nil {
		return store.Run{}, fmt.Errorf("client is stopping")
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return store.Run{}, ErrLocalBusy
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			<-s.slots
		}
	}()
	releaseDirectory, e := s.Store.HoldConversation(endpoint.LocalID, conversation)
	if e != nil {
		return store.Run{}, e
	}
	defer releaseDirectory()
	session, cwd, e := s.Sessions.Acquire(ctx, endpoint.LocalID, conversation, "local-"+la.Name, la.Name, la.Revision)
	if e != nil {
		return store.Run{}, e
	}
	run := store.Run{EndpointID: endpoint.LocalID, ConversationID: conversation, SessionID: session.ID, RunID: uuid.NewString(), AttemptID: uuid.NewString(), AgentID: session.AgentID, Harness: la.Name, HarnessRevision: la.Revision, Directory: cwd, Summary: request.Prompt, ReceivedAt: time.Now().UnixMilli(), Status: "running"}
	if run.Summary == "" {
		run.Summary = string(input)
	}
	writer := s.Store.ForEndpoint(endpoint.LocalID)
	if e = writer.Receive(ctx, run); e != nil {
		_ = s.Store.ReleaseSession(session, 0, true)
		return store.Run{}, e
	}
	resource, e := store.SafePath(s.Store.Root, endpoint.LocalID, conversation, "sessions", session.ID, "runs", run.RunID, run.AttemptID, "input.json")
	if e == nil {
		e = store.Atomic(resource, input)
	}
	if e == nil {
		la.HistoryPath = filepath.Join(filepath.Dir(resource), "history.json")
		e = store.Atomic(la.HistoryPath, []byte("[]"))
	}
	if e != nil {
		_ = writer.Complete(context.Background(), run.RunID, run.AttemptID, store.Finish{Status: "failed", Error: e.Error()})
		_ = s.Store.ReleaseSession(session, 0, true)
		return store.Run{}, e
	}
	la.EventsPath = harness.EventsPath(cwd, session.ID)
	la.Env = []string{"AGENTUSE_SERVER=", "AGENTUSE_TOKEN=", "AGENTUSE_WORK=", "AGENTUSE_CONFIG=", "AGENTUSE_ENDPOINT=local", "AGENTUSE_CONVERSATION=" + conversation, "AGENTUSE_SESSION=" + conversation, "AGENTUSE_RUN=" + run.RunID, "AGENTUSE_ATTEMPT=" + run.AttemptID, "AGENTUSE_AGENT=" + session.AgentID, "AGENTUSE_RESOURCES=" + filepath.Dir(resource)}
	execCtx, cancel := context.WithCancel(s.localCtx)
	canSteer := la.CanSteer()
	canAsk := la.CanAsk()
	execution := &localExecution{canSteer: canSteer, canAsk: canAsk, cancel: cancel, in: harness.NewInput(harness.DefaultAskTimeout), done: make(chan struct{}), session: session.ID}
	s.localRuns[run.RunID] = execution
	s.localWG.Add(1)
	releaseSlot = false
	go func() {
		defer s.localWG.Done()
		defer close(execution.done)
		defer cancel()
		defer func() { <-s.slots; s.localMu.Lock(); delete(s.localRuns, run.RunID); s.localMu.Unlock() }()
		result := harness.Result{}
		defer func() {
			status := "succeeded"
			if !result.OK {
				status = "failed"
			}
			if execCtx.Err() != nil {
				status = "cancelled"
			}
			c := context.Background()
			_ = writer.Complete(c, run.RunID, run.AttemptID, store.Finish{Status: status, Error: result.Error, Output: result.Output, Usage: result.Usage, CostUSD: result.CostUSD, HarnessRunIDs: result.HarnessRunIDs})
			_ = s.Store.Confirm(c, endpoint.LocalID, run.RunID, run.AttemptID)
			if !result.CleanupPending {
				_ = s.Sessions.Release(session, 0, result.OK || execCtx.Err() == context.Canceled, run.RunID, run.AttemptID)
			}
		}()
		report := func(kind string, payload any) {
			data, _ := json.Marshal(payload)
			_ = s.Store.Event(context.Background(), endpoint.LocalID, run.RunID, run.AttemptID, kind, data)
		}
		report("started", map[string]any{"mode": endpoint.LocalID, "harness_revision": la.Revision})
		dispatch := protocol.Dispatch{EndpointID: endpointID, Local: true, ID: s.sequence.Add(1), RunID: run.RunID, AttemptID: run.AttemptID, SessionID: conversation, AgentID: session.AgentID, Harness: la.Name, Input: input, SessionMessages: json.RawMessage("[]")}
		result = s.Harness.Execute(execCtx, la, dispatch, cwd, execution.in, report)

		kind := "done"
		if !result.OK {
			kind = "failed"
		}
		report(kind, result)
	}()
	return run, nil
}
func (s *Service) Control(ctx context.Context, endpointID, conversation, session, run, attempt, action string, payload json.RawMessage) error {
	if e := s.requireLocalEndpoint(endpointID); e != nil {
		return e
	}
	if _, e := s.Store.Reader().Detail(ctx, endpoint.LocalID, conversation, session, run, attempt); e != nil {
		return e
	}
	s.localMu.Lock()
	defer s.localMu.Unlock()
	execution := s.localRuns[run]
	if execution == nil || execution.session != session {
		return ErrLocalBusy
	}
	switch action {
	case "cancel":
		execution.cancel()
	case "steer":
		if !execution.canSteer {
			return fmt.Errorf("该适配器不支持运行中插话")
		}
		var v struct {
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal(payload, &v) != nil || strings.TrimSpace(v.Prompt) == "" {
			return store.ErrInvalid
		}
		id := s.sequence.Add(1)
		if !execution.in.Steer(protocol.Steer{ID: id, RunID: run, AttemptID: attempt, Prompt: v.Prompt}) {
			return ErrLocalBusy
		}
		return s.Store.ForEndpoint(endpoint.LocalID).Steer(ctx, run, attempt, id)
	case "answer":
		if !execution.canAsk {
			return fmt.Errorf("该适配器不支持提问回调")
		}
		var v struct {
			ToolUseID string            `json:"tool_use_id"`
			Answers   map[string]string `json:"answers"`
		}
		if json.Unmarshal(payload, &v) != nil || v.ToolUseID == "" {
			return store.ErrInvalid
		}
		if !execution.in.Answer(protocol.Answer{RunID: run, AttemptID: attempt, ToolUseID: v.ToolUseID, Answers: v.Answers}) {
			return ErrLocalBusy
		}
	default:
		return store.ErrInvalid
	}
	return nil
}

func (s *Service) localActiveCount() int {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	return len(s.localRuns)
}

func (s *Service) requireLocalEndpoint(id string) error {
	ep, e := s.Endpoints.GetByID(id)
	if e != nil {
		return e
	}
	if !ep.IsLocal() {
		return fmt.Errorf("platform tasks must be claimed from their endpoint")
	}
	return nil
}
func (s *Service) ProbeHarnesses() ([]harness.HarnessInfo, error) { return s.Harness.Probe() }
