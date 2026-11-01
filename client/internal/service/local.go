package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/harness"
	"github.com/memory-co/agentuse/internal/protocol"
)

var ErrLocalBusy = errors.New("local harness is busy")

type LocalRunRequest struct {
	Harness string          `json:"harness"`
	Prompt  string          `json:"prompt"`
	Input   json.RawMessage `json:"input,omitempty"`
}
type localExecution struct {
	canSteer bool
	canAsk   bool
	cancel   context.CancelFunc
	in       *inbox
	done     chan struct{}
	session  string
}
type LocalService struct {
	Files      *store.Ledger
	Sessions   *SessionService
	ConfigPath string
	Slots      chan struct{}
	mu         sync.Mutex
	active     map[string]*localExecution
	ctx        context.Context
	cancel     context.CancelFunc
	sequence   atomic.Int64
	wg         sync.WaitGroup
}

func NewLocalService(files *store.Ledger, sessions *SessionService, path string, slots chan struct{}) *LocalService {
	ctx, cancel := context.WithCancel(context.Background())
	return &LocalService{Files: files, Sessions: sessions, ConfigPath: path, Slots: slots, active: map[string]*localExecution{}, ctx: ctx, cancel: cancel}
}
func (s *LocalService) Close() { s.mu.Lock(); s.cancel(); s.mu.Unlock(); s.wg.Wait() }
func (s *LocalService) CreateConversation(ctx context.Context, title string) (store.Conversation, error) {
	if _, e := s.Sessions.Reclaim(ctx); e != nil {
		return store.Conversation{}, e
	}
	return s.Files.CreateLocalConversation(ctx, title)
}
func (s *LocalService) Harnesses() ([]HarnessInfo, error) {
	packages, e := (HarnessService{Root: HarnessRoot(Config{ConfigPath: s.ConfigPath})}).Packages()
	if e != nil {
		return nil, e
	}
	out := []HarnessInfo{}
	for name, la := range packages {
		item := HarnessInfo{Name: name, Revision: la.Revision, Disabled: la.Disabled}
		if !la.Disabled {
			if path, e := exec.LookPath(expandHome(la.Path)); e == nil {
				item.Path = path
				item.Found = true
				if la.Spec != nil && len(la.Spec.Detect.Version) > 0 {
					item.Version = probeVersion(path, la.Spec.Detect.Version)
				}
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *LocalService) Start(ctx context.Context, conversation string, request LocalRunRequest) (store.Run, error) {
	if _, e := s.Files.LocalConversation(ctx, conversation); e != nil {
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
	packages, e := (HarnessService{Root: HarnessRoot(Config{ConfigPath: s.ConfigPath})}).Packages()
	if e != nil {
		return store.Run{}, e
	}
	la, ok := packages[request.Harness]
	if !ok || la.Disabled {
		return store.Run{}, fmt.Errorf("harness is not installed or enabled")
	}
	la.Path, e = exec.LookPath(expandHome(la.Path))
	if e != nil {
		return store.Run{}, fmt.Errorf("harness program unavailable: %w", e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return store.Run{}, fmt.Errorf("client is stopping")
	}
	select {
	case s.Slots <- struct{}{}:
	default:
		return store.Run{}, ErrLocalBusy
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			<-s.Slots
		}
	}()
	releaseDirectory, e := s.Files.HoldConversation("local", conversation)
	if e != nil {
		return store.Run{}, e
	}
	defer releaseDirectory()
	session, cwd, e := s.Sessions.Acquire(ctx, "local", conversation, "local-"+la.Name, la.Name, la.Revision)
	if e != nil {
		return store.Run{}, e
	}
	run := store.Run{EndpointID: "local", ConversationID: conversation, SessionID: session.ID, RunID: uuid.NewString(), AttemptID: uuid.NewString(), AgentID: session.AgentID, Harness: la.Name, HarnessRevision: la.Revision, Directory: cwd, Summary: request.Prompt, ReceivedAt: time.Now().UnixMilli(), Status: "running"}
	if run.Summary == "" {
		run.Summary = string(input)
	}
	writer := s.Files.ForEndpoint("local")
	if e = writer.Receive(ctx, run); e != nil {
		_ = s.Files.ReleaseSession(session, "", 0, false)
		return store.Run{}, e
	}
	resource, e := store.SafePath(s.Files.Root, "local", conversation, "sessions", session.ID, "runs", run.RunID, run.AttemptID, "input.json")
	if e == nil {
		e = store.Atomic(resource, input)
	}
	if e == nil {
		la.HistoryPath = filepath.Join(filepath.Dir(resource), "history.json")
		e = store.Atomic(la.HistoryPath, []byte("[]"))
	}
	if e != nil {
		_ = writer.Complete(context.Background(), run.RunID, run.AttemptID, store.Finish{Status: "failed", Error: e.Error()})
		_ = s.Files.ReleaseSession(session, "", 0, false)
		return store.Run{}, e
	}
	la.ResumeID = session.NativeSessionID
	la.LocalSessionID = session.ID
	la.Env = []string{"AGENTUSE_SERVER=", "AGENTUSE_TOKEN=", "AGENTUSE_WORK=", "AGENTUSE_CONFIG=", "AGENTUSE_ENDPOINT=local", "AGENTUSE_CONVERSATION=" + conversation, "AGENTUSE_SESSION=" + conversation, "AGENTUSE_RUN=" + run.RunID, "AGENTUSE_ATTEMPT=" + run.AttemptID, "AGENTUSE_AGENT=" + session.AgentID, "AGENTUSE_RESOURCES=" + filepath.Dir(resource)}
	execCtx, cancel := context.WithCancel(s.ctx)
	canSteer := la.Spec != nil && (la.Spec.Transport == harness.TransportCodexAppServer || la.Spec.Steer != "")
	canAsk := la.Spec != nil && la.Spec.Ask != ""
	execution := &localExecution{canSteer: canSteer, canAsk: canAsk, cancel: cancel, in: &inbox{steers: &steerQueue{}, asks: newAskBox(defaultAskTimeout)}, done: make(chan struct{}), session: session.ID}
	s.active[run.RunID] = execution
	s.wg.Add(1)
	releaseSlot = false
	go func() {
		defer s.wg.Done()
		defer close(execution.done)
		defer cancel()
		defer func() { <-s.Slots; s.mu.Lock(); delete(s.active, run.RunID); s.mu.Unlock() }()
		result := Result{}
		defer func() {
			status := "succeeded"
			if !result.OK {
				status = "failed"
			}
			if execCtx.Err() != nil {
				status = "cancelled"
			}
			c := context.Background()
			_ = writer.Complete(c, run.RunID, run.AttemptID, store.Finish{Status: status, Error: result.Error, Output: result.Output, Usage: result.Usage, CostUSD: result.CostUSD})
			_ = s.Files.Confirm(c, "local", run.RunID, run.AttemptID)
			_ = s.Sessions.Release(session, result.SessionID, 0, result.OK, run.RunID, run.AttemptID)
		}()
		report := func(kind string, payload any) {
			data, _ := json.Marshal(payload)
			_ = s.Files.Event(context.Background(), "local", run.RunID, run.AttemptID, kind, data)
		}
		report("started", map[string]any{"mode": "local", "harness_revision": la.Revision})
		dispatch := protocol.Dispatch{Local: true, ID: s.sequence.Add(1), RunID: run.RunID, AttemptID: run.AttemptID, SessionID: conversation, AgentID: session.AgentID, Harness: la.Name, Input: input, SessionMessages: json.RawMessage("[]")}
		result = runHarness(execCtx, la, dispatch, cwd, execution.in, report)
		if session.NativeSessionID != "" && result.SessionID != "" && session.NativeSessionID != result.SessionID {
			result.OK = false
			result.Error = "harness returned a different native session ID while resuming"
			result.SessionID = ""
		}
		kind := "done"
		if !result.OK {
			kind = "failed"
		}
		report(kind, result)
	}()
	return run, nil
}
func (s *LocalService) Control(ctx context.Context, conversation, session, run, attempt, action string, payload json.RawMessage) error {
	if _, e := s.Files.Reader().Detail(ctx, "local", conversation, session, run, attempt); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	execution := s.active[run]
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
		execution.in.steers.push(protocol.Steer{ID: id, RunID: run, AttemptID: attempt, Prompt: v.Prompt})
		return s.Files.ForEndpoint("local").Steer(ctx, run, attempt, id)
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
		execution.in.asks.answer(protocol.Answer{RunID: run, AttemptID: attempt, ToolUseID: v.ToolUseID, Answers: v.Answers})
	default:
		return store.ErrInvalid
	}
	return nil
}

func (s *LocalService) ActiveCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.active) }
