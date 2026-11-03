package run

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/session"

	sdk "github.com/memory-co/agentuse/sdk/golang"
)

// Run is exactly one adapter-owned interaction. There is no client attempt ID
// or caller-assigned run identity. All execution facts come from events.jsonl.
type Run struct {
	EndpointID      string     `json:"endpoint_id"`
	ConversationID  string     `json:"conversation_id"`
	SessionID       string     `json:"session_id"`
	RunID           string     `json:"run_id"`
	AgentID         string     `json:"agent_id"`
	Harness         string     `json:"harness"`
	HarnessRevision string     `json:"harness_revision"`
	Summary         string     `json:"summary"`
	Status          string     `json:"status"`
	Output          string     `json:"output,omitempty"`
	Error           string     `json:"error,omitempty"`
	ReceivedAt      int64      `json:"received_at"`
	FinishedAt      *int64     `json:"finished_at,omitempty"`
	DurationMS      int64      `json:"duration_ms"`
	Usage           *sdk.Usage `json:"usage"`
	CostUSD         *float64   `json:"cost_usd"`
	Events          []Event    `json:"events,omitempty"`
}
type nativeReceipt struct {
	Type            string `json:"type"`
	At              int64  `json:"at"`
	RunID           string `json:"run_id"`
	EndpointID      string `json:"endpoint_id"`
	ConversationID  string `json:"conversation_id"`
	SessionID       string `json:"session_id"`
	AgentID         string `json:"agent_id"`
	Harness         string `json:"harness"`
	HarnessRevision string `json:"harness_revision"`
}

func (l *Service) AcceptNative(ctx context.Context, s session.Session, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if parsed, e := uuid.Parse(id); e != nil || parsed.String() != id {
		return fileutil.ErrInvalid
	}
	path, err := fileutil.SafePath(l.Sessions.Root, s.EndpointID, s.ConversationID, "sessions", s.ID, "runs", id+".jsonl")
	if err != nil {
		return err
	}
	l.fileMu.Lock()
	defer l.fileMu.Unlock()
	if _, err = os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	receipt := nativeReceipt{Type: "reference", At: time.Now().UnixMilli(), RunID: id, EndpointID: s.EndpointID, ConversationID: s.ConversationID, SessionID: s.ID, AgentID: s.AgentID, Harness: s.Harness, HarnessRevision: s.HarnessRevision}
	return fileutil.Atomic(path, append(raw(receipt), '\n'))
}
func (r *Service) Detail(ctx context.Context, endpoint, conversation, session, id string) (Run, error) {
	var result Run
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if parsed, e := uuid.Parse(id); e != nil || parsed.String() != id {
		return result, fileutil.ErrInvalid
	}
	path, err := fileutil.SafePath(r.Sessions.Root, endpoint, conversation, "sessions", session, "runs", id+".jsonl")
	if err != nil {
		return result, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	var receipt nativeReceipt
	if err = json.Unmarshal(data, &receipt); err != nil {
		return result, err
	}
	if receipt.Type != "reference" || receipt.RunID != id || receipt.EndpointID != endpoint || receipt.ConversationID != conversation || receipt.SessionID != session {
		return result, fmt.Errorf("native run identity mismatch")
	}

	result = Run{EndpointID: endpoint, ConversationID: conversation, SessionID: session, RunID: id, AgentID: receipt.AgentID, Harness: receipt.Harness, HarnessRevision: receipt.HarnessRevision, Status: "running", ReceivedAt: receipt.At}
	source := filepath.Join(filepath.Dir(filepath.Dir(path)), "events.jsonl")
	if _, err = fileutil.SafePath(filepath.Dir(source), filepath.Base(source)); err != nil {
		return result, err
	}
	file, err := os.Open(source)
	if err != nil {
		return result, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	pending := map[string]bool{}
	first := true
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		var event struct {
			Type, Text, State, Status, Output string
			RunID                             string `json:"run_id"`
			RequestID                         string `json:"request_id"`
			TS                                string `json:"ts"`
			Usage                             *sdk.Usage
			CostUSD                           *float64 `json:"cost_usd"`
			Error                             *string
		}
		if err = json.Unmarshal(line, &event); err != nil {
			return result, err
		}
		if event.RunID != id {
			continue
		}
		at := receipt.At
		if stamp, e := time.Parse(time.RFC3339Nano, event.TS); e == nil {
			at = stamp.UnixMilli()
		}
		result.Events = append(result.Events, Event{Kind: event.Type, At: at, Payload: json.RawMessage(line)})
		switch event.Type {
		case "accepted":
			if first {
				first = false
				result.ReceivedAt = at
				text := event.Text
				if _, task, found := strings.Cut(text, "\n\n# 本次任务\n"); found {
					text = task
				}
				chars := []rune(text)
				if len(chars) > 200 {
					chars = chars[:200]
				}
				result.Summary = string(chars)
			}
		case "request":
			if event.State == "pending" {
				pending[event.RequestID] = true
			} else {
				delete(pending, event.RequestID)
			}
		case "result":
			result.Status = event.Status
			result.Output = event.Output
			result.Usage = event.Usage
			result.CostUSD = event.CostUSD
			if event.Error != nil {
				result.Error = *event.Error
			}
			result.FinishedAt = &at
			result.DurationMS = max(0, at-result.ReceivedAt)
		}
	}
	if result.FinishedAt == nil && len(pending) > 0 {
		result.Status = "waiting"
	}

	return result, nil
}
func (r *Service) List(ctx context.Context, f Filter) (Page, error) {
	page := Page{Runs: []Run{}, Limit: f.Limit, Offset: f.Offset}
	if page.Limit <= 0 || page.Limit > 200 {
		page.Limit = 50
	}
	root, err := fileutil.SafePath(r.Sessions.Root, f.Endpoint, f.Conversation, "sessions", f.Session, "runs")
	if err != nil {
		return page, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return page, nil
	}
	if err != nil {
		return page, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		run, err := r.Detail(ctx, f.Endpoint, f.Conversation, f.Session, id)
		if err != nil {
			return page, err
		}
		if f.Status != "" && run.Status != f.Status || f.Agent != "" && run.AgentID != f.Agent || f.After > 0 && run.ReceivedAt < f.After || f.Before > 0 && run.ReceivedAt >= f.Before {
			continue
		}
		page.Runs = append(page.Runs, run)
	}
	sort.Slice(page.Runs, func(i, j int) bool { return page.Runs[i].RunID < page.Runs[j].RunID })
	page.Total = len(page.Runs)
	start := min(max(0, f.Offset), page.Total)
	end := min(start+page.Limit, page.Total)
	page.Runs = page.Runs[start:end]
	if end < page.Total {
		page.NextCursor = fmt.Sprint(end)
	}
	return page, nil
}

// Rebuild is deterministic: every accepted UUID in a session belongs to that
// session. No caller task ID, attempt or input-to-run prediction is involved.
func (l *Service) rebuildNativeIndexes(ctx context.Context) error {
	endpoints, err := os.ReadDir(l.Sessions.Root)
	if err != nil {
		return err
	}
	for _, ep := range endpoints {
		if !ep.IsDir() {
			continue
		}
		conversations, err := l.Sessions.Conversations(ctx, ep.Name())
		if err != nil {
			return err
		}
		for _, c := range conversations {
			sessions, err := l.Sessions.Sessions(ctx, ep.Name(), c.ID)
			if err != nil {
				return err
			}
			for _, session := range sessions {
				path, err := fileutil.SafePath(l.Sessions.Root, ep.Name(), c.ID, "sessions", session.ID, "events.jsonl")
				if err != nil {
					return err
				}
				file, err := os.Open(path)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				reader := bufio.NewReader(file)
				for {
					line, e := reader.ReadBytes('\n')
					if e == io.EOF {
						break
					}
					if e != nil {
						file.Close()
						return e
					}
					var event struct {
						Type  string
						RunID string `json:"run_id"`
					}
					if e = json.Unmarshal(line, &event); e != nil {
						file.Close()
						return e
					}
					if event.Type == "accepted" {
						if e = l.AcceptNative(ctx, session, event.RunID); e != nil {
							file.Close()
							return e
						}
					}
				}
				file.Close()
			}
		}
	}
	return nil
}

func (r *Service) Stats(ctx context.Context, group string, f Filter) ([]Statistic, error) {
	if group != "harness" && group != "day" && group != "agent" {
		return nil, fileutil.ErrInvalid
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
