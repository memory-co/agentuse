// Package store persists client state in YAML/JSON files. Readers expose only
// parent-scoped queries; there is no client database or persistent index.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

type Run struct {
	EventsOffset    *int64 `json:"events_offset,omitempty"`
	legacy          bool
	HarnessRunIDs   []string        `json:"harness_run_ids,omitempty"`
	ServerConfirmed bool            `json:"server_confirmed"`
	PendingReport   json.RawMessage `json:"pending_report,omitempty"`
	EndpointID      string          `json:"endpoint_id"`
	ConversationID  string          `json:"conversation_id"`
	RunID           string          `json:"run_id"`
	AttemptID       string          `json:"attempt_id"`
	Server          string          `json:"server,omitempty"`
	DaemonID        string          `json:"device_id,omitempty"`
	Sender          agentuse.Sender `json:"sender"`
	AgentID         string          `json:"agent_id"`
	SessionID       string          `json:"session_id"`
	WorkID          string          `json:"work_id,omitempty"`
	Harness         string          `json:"harness"`
	HarnessRevision string          `json:"harness_revision,omitempty"`
	Summary         string          `json:"summary"`
	Directory       string          `json:"directory,omitempty"`
	ReceivedAt      int64           `json:"received_at"`
	FinishedAt      *int64          `json:"finished_at,omitempty"`
	DurationMS      int64           `json:"duration_ms"`
	Status          string          `json:"status"`
	Error           string          `json:"error,omitempty"`
	Output          string          `json:"output,omitempty"`
	SteerCount      int             `json:"steer_count"`
	SteerIDs        []int64         `json:"steer_ids,omitempty"`
	AttachmentCount int             `json:"attachment_count"`
	Usage           *agentuse.Usage `json:"usage"`
	CostUSD         *float64        `json:"cost_usd"`
	Events          []Event         `json:"events,omitempty"`
}
type Event struct {
	Kind    string          `json:"kind"`
	At      int64           `json:"at"`
	Payload json.RawMessage `json:"payload"`
}
type Finish struct {
	HarnessRunIDs   []string
	Status, Error   string
	AttachmentCount int
}
type Filter struct {
	Endpoint, Conversation, Session string
	Sender, Agent, Status           string
	After, Before                   int64
	Limit, Offset                   int
}
type Page struct {
	Runs       []Run  `json:"items"`
	Total      int    `json:"total"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type Statistic struct {
	Key       string         `json:"key"`
	Runs      int            `json:"runs"`
	UsageRuns int            `json:"usage_runs"`
	Usage     agentuse.Usage `json:"usage"`
	CostUSD   float64        `json:"cost_usd"`
}
type Writer interface {
	Receive(context.Context, Run) error
	Complete(context.Context, string, string, Finish) error
	Steer(context.Context, string, string, int64) error
	Reconcile(context.Context) error
}
type Reader interface {
	List(context.Context, Filter) (Page, error)
	Stats(context.Context, string, Filter) ([]Statistic, error)
	Conversations(context.Context, string) ([]Conversation, error)
	Sessions(context.Context, string, string) ([]Session, error)
	Session(context.Context, string, string, string) (Session, error)
	Detail(context.Context, string, string, string, string, string) (Run, error)
}
type Ledger struct {
	reserved map[string]int
	Root     string
	mu       sync.Mutex
	paths    map[string]string
}
type writeStore struct {
	l        *Ledger
	endpoint string
}
type readStore struct{ l *Ledger }

func Open(root string) (*Ledger, error) {
	p, e := SafePath(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(p, 0700); e != nil {
		return nil, e
	}
	return &Ledger{Root: p, paths: map[string]string{}}, nil
}
func (l *Ledger) Writer() Writer               { return &writeStore{l: l} }
func (l *Ledger) ForEndpoint(id string) Writer { return &writeStore{l: l, endpoint: id} }
func (l *Ledger) Reader() Reader               { return &readStore{l} }
func (l *Ledger) Close() error                 { return nil }
func key(endpoint, run, attempt string) string { return endpoint + "/" + run + "/" + attempt }
func (w *writeStore) Receive(ctx context.Context, r Run) error {
	if !Segment(r.RunID) || !Segment(r.AttemptID) {
		return ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	w.l.mu.Lock()
	defer w.l.mu.Unlock()
	if r.EndpointID == "" {
		r.EndpointID = w.endpoint
	}
	if r.EndpointID == "" {
		_, r.EndpointID, _ = NormalizeEndpoint(r.Server)
	}
	p, e := SafePath(w.l.Root, r.EndpointID, r.ConversationID, "sessions", r.SessionID, "runs", r.RunID+".jsonl")
	if e != nil {
		return e
	}
	if r.ReceivedAt == 0 {
		r.ReceivedAt = time.Now().UnixMilli()
	}
	rs := []rune(r.Summary)
	if len(rs) > 200 {
		r.Summary = string(rs[:200])
	}
	r.Status = "running"
	offset := int64(0)
	source := filepath.Join(filepath.Dir(filepath.Dir(p)), "events.jsonl")
	if info, err := os.Stat(source); err == nil {
		offset = info.Size()
	} else if !os.IsNotExist(err) {
		return err
	}
	r.EventsOffset = &offset
	var existing Run
	existing.AttemptID = r.AttemptID
	if err := readRunMetadata(p, &existing); errors.Is(err, os.ErrNotExist) {
		if e = appendRun(p, "received", r.AttemptID, metadata(r)); e != nil {
			return e
		}
	} else if err != nil {
		return err
	}

	w.l.paths[key(r.EndpointID, r.RunID, r.AttemptID)] = p
	return nil
}
func (w *writeStore) update(ctx context.Context, run, attempt string, fn func(*Run)) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	w.l.mu.Lock()
	defer w.l.mu.Unlock()
	p := w.l.paths[key(w.endpoint, run, attempt)]
	if p == "" && w.endpoint == "" {
		for k, path := range w.l.paths {
			if strings.HasSuffix(k, "/"+run+"/"+attempt) {
				if p != "" {
					return ErrInvalid
				}
				p = path
			}
		}
	}
	if p == "" {
		return os.ErrNotExist
	}
	r := Run{AttemptID: attempt}
	if e := readRunMetadata(p, &r); e != nil {
		return e
	}
	before := metadata(r)
	fn(&r)
	changes := metadataDiff(before, metadata(r))
	if len(changes) == 0 {
		return nil
	}
	return appendRun(p, "updated", attempt, changes)
}
func (w *writeStore) Complete(ctx context.Context, run, attempt string, f Finish) error {
	return w.update(ctx, run, attempt, func(r *Run) {
		now := time.Now().UnixMilli()
		r.Status = f.Status
		r.Error = f.Error
		if len(f.HarnessRunIDs) > 0 {
			r.HarnessRunIDs = f.HarnessRunIDs
		}
		r.FinishedAt = &now
		r.DurationMS = max(0, now-r.ReceivedAt)

		r.AttachmentCount = f.AttachmentCount
	})
}
func (w *writeStore) Steer(ctx context.Context, run, attempt string, id int64) error {
	return w.update(ctx, run, attempt, func(r *Run) {
		for _, v := range r.SteerIDs {
			if v == id {
				return
			}
		}
		r.SteerIDs = append(r.SteerIDs, id)
		r.SteerCount = len(r.SteerIDs)
	})
}
func (l *Ledger) Event(ctx context.Context, endpoint, run, attempt, kind string, payload json.RawMessage) error {
	if kind != "accepted" {
		return nil
	}
	var accepted struct {
		RunID string `json:"harness_run_id"`
	}
	if err := json.Unmarshal(payload, &accepted); err != nil {
		return err
	}
	if accepted.RunID == "" {
		return ErrInvalid
	}
	return (&writeStore{l: l, endpoint: endpoint}).update(ctx, run, attempt, func(r *Run) {
		for _, id := range r.HarnessRunIDs {
			if id == accepted.RunID {
				return
			}
		}
		r.HarnessRunIDs = append(r.HarnessRunIDs, accepted.RunID)
	})
}
func (w *writeStore) Reconcile(ctx context.Context) error {
	if err := w.l.migrateRuns(ctx); err != nil {
		return err
	}
	return filepath.WalkDir(w.l.Root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(w.l.Root, path)
			parts := strings.Split(rel, string(filepath.Separator))
			if (len(parts) == 3 && parts[2] == "workspace") || (len(parts) == 5 && parts[4] == "events.jsonl.d") {
				return filepath.SkipDir
			}
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in client state")
		}
		if !isRunRecord(w.l.Root, path) {
			return nil
		}
		runs, e := readRunJournal(path)
		if e != nil {
			return e
		}
		w.l.mu.Lock()
		defer w.l.mu.Unlock()
		for _, r := range runs {
			if w.endpoint != "" && r.EndpointID != w.endpoint {
				continue
			}
			w.l.paths[key(r.EndpointID, r.RunID, r.AttemptID)] = path
			if r.Status == "running" || r.Status == "waiting" {
				if len(r.HarnessRunIDs) == 0 && r.EventsOffset != nil {
					source := filepath.Join(filepath.Dir(filepath.Dir(path)), "events.jsonl")
					r.HarnessRunIDs, e = recoverBindings(source, *r.EventsOffset)
					if e != nil {
						return e
					}
				}
				now := time.Now().UnixMilli()
				r.Status = "interrupted"
				r.Error = "client restarted before execution completed"
				r.FinishedAt = &now
				r.DurationMS = max(0, now-r.ReceivedAt)
				if e = appendRun(path, "updated", r.AttemptID, map[string]json.RawMessage{"status": raw(r.Status), "error": raw(r.Error), "finished_at": raw(now), "duration_ms": raw(r.DurationMS), "harness_run_ids": raw(r.HarnessRunIDs)}); e != nil {
					return e
				}
			}
		}
		return nil
	})
}

func (r *readStore) Detail(ctx context.Context, endpoint, conversation, session, run, attempt string) (Run, error) {
	v := Run{AttemptID: attempt}
	if e := ctx.Err(); e != nil {
		return v, e
	}
	p, e := SafePath(r.l.Root, endpoint, conversation, "sessions", session, "runs", run+".jsonl")
	if e != nil {
		return v, e
	}
	e = readRun(p, &v)
	if e == nil && (v.EndpointID != endpoint || v.ConversationID != conversation || v.SessionID != session || v.RunID != run || v.AttemptID != attempt) {
		e = fmt.Errorf("run identity mismatch")
	}
	return v, e
}
func (r *readStore) List(ctx context.Context, f Filter) (Page, error) {
	p := Page{Runs: []Run{}, Limit: f.Limit, Offset: f.Offset}
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	root, e := SafePath(r.l.Root, f.Endpoint, f.Conversation, "sessions", f.Session, "runs")
	if e != nil {
		return p, e
	}
	entries, e := os.ReadDir(root)
	if errors.Is(e, os.ErrNotExist) {
		return p, nil
	}
	if e != nil {
		return p, e
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !Segment(runID) {
			return p, ErrInvalid
		}
		path, e := SafePath(root, entry.Name())
		if e != nil {
			return p, e
		}
		runs, e := readRunJournal(path)
		if e != nil {
			return p, e
		}
		for _, item := range runs {
			v, e := r.Detail(ctx, f.Endpoint, f.Conversation, f.Session, runID, item.AttemptID)
			if e != nil {
				return p, e
			}
			if f.Status != "" && v.Status != f.Status || f.Agent != "" && v.AgentID != f.Agent || f.After > 0 && v.ReceivedAt < f.After || f.Before > 0 && v.ReceivedAt >= f.Before {
				continue
			}
			p.Runs = append(p.Runs, v)
		}
	}
	sort.Slice(p.Runs, func(i, j int) bool {
		return p.Runs[i].RunID+"/"+p.Runs[i].AttemptID < p.Runs[j].RunID+"/"+p.Runs[j].AttemptID
	})
	p.Total = len(p.Runs)
	start := min(max(0, f.Offset), p.Total)
	end := min(start+p.Limit, p.Total)
	p.Runs = p.Runs[start:end]
	if end < p.Total {
		p.NextCursor = fmt.Sprint(end)
	}
	return p, nil
}
func (r *readStore) Stats(ctx context.Context, group string, f Filter) ([]Statistic, error) {
	if group != "harness" && group != "day" && group != "agent" && group != "sender" {
		return nil, ErrInvalid
	}
	counts := map[string]*Statistic{}
	f.Limit = 200
	for {
		p, e := r.List(ctx, f)
		if e != nil {
			return nil, e
		}
		for _, v := range p.Runs {
			k := v.Harness
			switch group {
			case "day":
				k = time.UnixMilli(v.ReceivedAt).UTC().Format("2006-01-02")
			case "agent":
				k = v.AgentID
			case "sender":
				k = v.Sender.ID
			}
			if counts[k] == nil {
				counts[k] = &Statistic{Key: k}
			}
			s := counts[k]
			s.Runs++
			if v.Usage != nil {
				s.UsageRuns++
				s.Usage.InputTokens += v.Usage.InputTokens
				s.Usage.OutputTokens += v.Usage.OutputTokens
				s.Usage.CacheReadTokens += v.Usage.CacheReadTokens
				s.Usage.CacheWriteTokens += v.Usage.CacheWriteTokens
			}
			if v.CostUSD != nil {
				s.CostUSD += *v.CostUSD
			}
		}
		f.Offset += len(p.Runs)
		if f.Offset >= p.Total {
			break
		}
	}
	out := []Statistic{}
	for _, s := range counts {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (l *Ledger) PrepareReport(ctx context.Context, endpoint, run, attempt string, b json.RawMessage) error {
	var message map[string]json.RawMessage
	if e := json.Unmarshal(b, &message); e != nil {
		return e
	}
	delete(message, "payload")
	b, _ = json.Marshal(message)
	return (&writeStore{l: l, endpoint: endpoint}).update(ctx, run, attempt, func(r *Run) { r.PendingReport = b; r.ServerConfirmed = false })
}
func (l *Ledger) Confirm(ctx context.Context, endpoint, run, attempt string) error {
	w := &writeStore{l: l, endpoint: endpoint}
	var snapshot Run
	if err := w.update(ctx, run, attempt, func(r *Run) { r.ServerConfirmed = true; r.PendingReport = nil; snapshot = *r }); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, e := l.Reader().Session(ctx, endpoint, snapshot.ConversationID, snapshot.SessionID)
	if e != nil {
		return e
	}
	if s.Status == "pending_report" {
		s.Status = "idle"
		return l.SaveSession(s)
	}
	return nil
}
func (l *Ledger) Pending(endpoint string) []json.RawMessage {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []json.RawMessage{}
	for k, p := range l.paths {
		parts := strings.Split(k, "/")
		r := Run{AttemptID: parts[len(parts)-1]}
		if readRun(p, &r) == nil && r.EndpointID == endpoint && len(r.PendingReport) > 0 && !r.ServerConfirmed {
			var message map[string]json.RawMessage
			json.Unmarshal(r.PendingReport, &message)
			message["payload"] = resultPayload(r)
			data, _ := json.Marshal(message)
			out = append(out, data)
		}
	}
	return out
}

func isRunRecord(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	if e != nil {
		return false
	}
	p := strings.Split(rel, string(filepath.Separator))
	return len(p) == 6 && p[2] == "sessions" && p[4] == "runs" && strings.HasSuffix(p[5], ".jsonl") && Segment(strings.TrimSuffix(p[5], ".jsonl"))
}

func (l *Ledger) RejectReport(ctx context.Context, endpoint, run, attempt, reason string) error {
	var record Run
	w := &writeStore{l: l, endpoint: endpoint}
	if e := w.update(ctx, run, attempt, func(r *Run) {
		if len(r.PendingReport) > 0 {
			r.PendingReport = nil
			r.Status = "interrupted"
			r.Error = "server rejected stale result: " + reason
		}
		record = *r
	}); e != nil {
		return e
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, e := l.Reader().Session(ctx, endpoint, record.ConversationID, record.SessionID)
	if e != nil {
		return e
	}
	if s.Status == "pending_report" {
		s.Status = "unavailable"
		return l.SaveSession(s)
	}
	return nil
}
