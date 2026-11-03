// Compatibility storage for the unchanged server wire protocol. Delivery IDs
// and attempts belong only to that protocol, never to public client runs.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

type Delivery struct {
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
type DeliveryFinish struct {
	HarnessRunIDs   []string
	Status, Error   string
	AttachmentCount int
}
type deliverySink interface {
	Receive(context.Context, Delivery) error
	Complete(context.Context, string, string, DeliveryFinish) error
	Steer(context.Context, string, string, int64) error
	Reconcile(context.Context) error
}
type DeliveryPage struct {
	Runs                 []Delivery `json:"items"`
	Total, Limit, Offset int
	NextCursor           string
}
type serverDeliverySink struct {
	l        *Service
	endpoint string
}

func (l *Service) ServerWriter() deliverySink { return &serverDeliverySink{l: l} }
func (l *Service) ServerForEndpoint(id string) deliverySink {
	return &serverDeliverySink{l: l, endpoint: id}
}
func key(endpoint, run, attempt string) string { return endpoint + "/" + run + "/" + attempt }
func (w *serverDeliverySink) Receive(ctx context.Context, r Delivery) error {
	if !fileutil.Segment(r.RunID) || !fileutil.Segment(r.AttemptID) {
		return fileutil.ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	w.l.fileMu.Lock()
	defer w.l.fileMu.Unlock()
	if r.EndpointID == "" {
		r.EndpointID = w.endpoint
	}
	if r.EndpointID == "" {
		_, r.EndpointID, _ = endpoint.NormalizeEndpoint(r.Server)
	}
	p, e := fileutil.SafePath(w.l.Sessions.Root, r.EndpointID, r.ConversationID, "sessions", r.SessionID, ".server", r.RunID+".jsonl")
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
	var existing Delivery
	existing.AttemptID = r.AttemptID
	if err := readDeliveryMetadata(p, &existing); errors.Is(err, os.ErrNotExist) {
		if e = appendDelivery(p, "received", r.AttemptID, deliveryMetadata(r)); e != nil {
			return e
		}
	} else if err != nil {
		return err
	}

	w.l.serverPaths[key(r.EndpointID, r.RunID, r.AttemptID)] = p
	return nil
}
func (w *serverDeliverySink) update(ctx context.Context, run, attempt string, fn func(*Delivery)) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	w.l.fileMu.Lock()
	defer w.l.fileMu.Unlock()
	p := w.l.serverPaths[key(w.endpoint, run, attempt)]
	if p == "" && w.endpoint == "" {
		for k, path := range w.l.serverPaths {
			if strings.HasSuffix(k, "/"+run+"/"+attempt) {
				if p != "" {
					return fileutil.ErrInvalid
				}
				p = path
			}
		}
	}
	if p == "" {
		return os.ErrNotExist
	}
	r := Delivery{AttemptID: attempt}
	if e := readDeliveryMetadata(p, &r); e != nil {
		return e
	}
	before := deliveryMetadata(r)
	fn(&r)
	changes := deliveryDiff(before, deliveryMetadata(r))
	if len(changes) == 0 {
		return nil
	}
	return appendDelivery(p, "updated", attempt, changes)
}
func (w *serverDeliverySink) Complete(ctx context.Context, run, attempt string, f DeliveryFinish) error {
	return w.update(ctx, run, attempt, func(r *Delivery) {
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
func (w *serverDeliverySink) Steer(ctx context.Context, run, attempt string, id int64) error {
	return w.update(ctx, run, attempt, func(r *Delivery) {
		for _, v := range r.SteerIDs {
			if v == id {
				return
			}
		}
		r.SteerIDs = append(r.SteerIDs, id)
		r.SteerCount = len(r.SteerIDs)
	})
}
func (l *Service) Event(ctx context.Context, endpoint, run, attempt, kind string, payload json.RawMessage) error {
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
		return fileutil.ErrInvalid
	}
	return (&serverDeliverySink{l: l, endpoint: endpoint}).update(ctx, run, attempt, func(r *Delivery) {
		for _, id := range r.HarnessRunIDs {
			if id == accepted.RunID {
				return
			}
		}
		r.HarnessRunIDs = append(r.HarnessRunIDs, accepted.RunID)
	})
}
func (w *serverDeliverySink) Reconcile(ctx context.Context) error {
	if err := w.l.migrateRuns(ctx); err != nil {
		return err
	}
	err := filepath.WalkDir(w.l.Sessions.Root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(w.l.Sessions.Root, path)
			parts := strings.Split(rel, string(filepath.Separator))
			if (len(parts) == 3 && parts[2] == "workspace") || (len(parts) == 5 && parts[4] == "events.jsonl.d") {
				return filepath.SkipDir
			}
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in client state")
		}
		if !isDeliveryRecord(w.l.Sessions.Root, path) {
			return nil
		}
		runs, e := readDeliveryJournal(path)
		if e != nil {
			return e
		}
		w.l.fileMu.Lock()
		defer w.l.fileMu.Unlock()
		for _, r := range runs {
			if w.endpoint != "" && r.EndpointID != w.endpoint {
				continue
			}
			w.l.serverPaths[key(r.EndpointID, r.RunID, r.AttemptID)] = path
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
				if e = appendDelivery(path, "updated", r.AttemptID, map[string]json.RawMessage{"status": raw(r.Status), "error": raw(r.Error), "finished_at": raw(now), "duration_ms": raw(r.DurationMS), "harness_run_ids": raw(r.HarnessRunIDs)}); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
func (r *Service) DeliveryDetail(ctx context.Context, endpoint, conversation, session, run, attempt string) (Delivery, error) {
	v := Delivery{AttemptID: attempt}
	if e := ctx.Err(); e != nil {
		return v, e
	}
	p, e := fileutil.SafePath(r.Sessions.Root, endpoint, conversation, "sessions", session, ".server", run+".jsonl")
	if e != nil {
		return v, e
	}
	e = readDelivery(p, &v)
	if e == nil && (v.EndpointID != endpoint || v.ConversationID != conversation || v.SessionID != session || v.RunID != run || v.AttemptID != attempt) {
		e = fmt.Errorf("run identity mismatch")
	}
	return v, e
}
func (r *Service) DeliveryList(ctx context.Context, f Filter) (DeliveryPage, error) {
	p := DeliveryPage{Runs: []Delivery{}, Limit: f.Limit, Offset: f.Offset}
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	root, e := fileutil.SafePath(r.Sessions.Root, f.Endpoint, f.Conversation, "sessions", f.Session, ".server")
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
		if !fileutil.Segment(runID) {
			return p, fileutil.ErrInvalid
		}
		path, e := fileutil.SafePath(root, entry.Name())
		if e != nil {
			return p, e
		}
		runs, e := readDeliveryJournal(path)
		if e != nil {
			return p, e
		}
		for _, item := range runs {
			v, e := r.DeliveryDetail(ctx, f.Endpoint, f.Conversation, f.Session, runID, item.AttemptID)
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
func (l *Service) PrepareReport(ctx context.Context, endpoint, run, attempt string, b json.RawMessage) error {
	var message map[string]json.RawMessage
	if e := json.Unmarshal(b, &message); e != nil {
		return e
	}
	delete(message, "payload")
	b, _ = json.Marshal(message)
	return (&serverDeliverySink{l: l, endpoint: endpoint}).update(ctx, run, attempt, func(r *Delivery) { r.PendingReport = b; r.ServerConfirmed = false })
}
func (l *Service) Confirm(ctx context.Context, endpoint, run, attempt string) error {
	return (&serverDeliverySink{l: l, endpoint: endpoint}).update(ctx, run, attempt, func(r *Delivery) { r.ServerConfirmed = true; r.PendingReport = nil })
}
func (l *Service) Pending(endpoint string) []json.RawMessage {
	l.fileMu.Lock()
	defer l.fileMu.Unlock()
	out := []json.RawMessage{}
	for k, p := range l.serverPaths {
		parts := strings.Split(k, "/")
		r := Delivery{AttemptID: parts[len(parts)-1]}
		if readDelivery(p, &r) == nil && r.EndpointID == endpoint && len(r.PendingReport) > 0 && !r.ServerConfirmed {
			var message map[string]json.RawMessage
			json.Unmarshal(r.PendingReport, &message)
			message["payload"] = resultPayload(r)
			data, _ := json.Marshal(message)
			out = append(out, data)
		}
	}
	return out
}

func isDeliveryRecord(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	if e != nil {
		return false
	}
	p := strings.Split(rel, string(filepath.Separator))
	return len(p) == 6 && p[2] == "sessions" && p[4] == ".server" && strings.HasSuffix(p[5], ".jsonl") && fileutil.Segment(strings.TrimSuffix(p[5], ".jsonl"))
}

func (l *Service) RejectReport(ctx context.Context, endpoint, run, attempt, reason string) error {
	return (&serverDeliverySink{l: l, endpoint: endpoint}).update(ctx, run, attempt, func(r *Delivery) {
		if len(r.PendingReport) > 0 {
			r.PendingReport = nil
			r.Status = "interrupted"
			r.Error = "server rejected stale result: " + reason
		}
	})
}
