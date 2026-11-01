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

	"github.com/google/uuid"
)

type Session struct {
	ID              string `json:"id" yaml:"id"`
	EndpointID      string `json:"endpoint_id" yaml:"endpoint_id"`
	ConversationID  string `json:"conversation_id" yaml:"conversation_id"`
	AgentID         string `json:"agent_id" yaml:"agent_id"`
	Harness         string `json:"harness" yaml:"harness"`
	HarnessRevision string `json:"harness_revision" yaml:"harness_revision"`
	Status          string `json:"status" yaml:"status"`
	LastContextSeq  int64  `json:"last_context_seq" yaml:"last_context_seq"`
	CreatedAt       int64  `json:"created_at" yaml:"created_at"`
	LastUsedAt      int64  `json:"last_used_at" yaml:"last_used_at"`
	UpdatedAt       int64  `json:"updated_at" yaml:"updated_at"`
}
type Conversation struct {
	Title      string `json:"title,omitempty"`
	ID         string `json:"id"`
	EndpointID string `json:"endpoint_id"`
	Workspace  string `json:"workspace"`
}

func (r *readStore) Conversations(ctx context.Context, endpoint string) ([]Conversation, error) {
	root, e := SafePath(r.l.Root, endpoint)
	if e != nil {
		return nil, e
	}
	out := []Conversation{}
	ds, e := os.ReadDir(root)
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	for _, d := range ds {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if d.IsDir() && Segment(d.Name()) {
			if _, e = SafePath(root, d.Name()); e != nil {
				return nil, e
			}
			c := Conversation{ID: d.Name(), EndpointID: endpoint, Workspace: filepath.Join(endpoint, d.Name(), "workspace")}
			if endpoint == "local" {
				c, e = r.l.LocalConversation(ctx, d.Name())
				if errors.Is(e, os.ErrNotExist) {
					continue
				}
				if e != nil {
					return nil, e
				}
			}
			out = append(out, c)
		}
	}
	return out, nil
}
func (r *readStore) Session(ctx context.Context, endpoint, conversation, id string) (Session, error) {
	var s Session
	if e := ctx.Err(); e != nil {
		return s, e
	}
	p, e := SafePath(r.l.Root, endpoint, conversation, "sessions", id, "session.yaml")
	if e != nil {
		return s, e
	}
	e = ReadYAML(p, &s)
	if e == nil && (s.ID != id || s.EndpointID != endpoint || s.ConversationID != conversation) {
		e = fmt.Errorf("session identity mismatch")
	}
	return s, e
}
func (r *readStore) Sessions(ctx context.Context, endpoint, conversation string) ([]Session, error) {
	root, e := SafePath(r.l.Root, endpoint, conversation, "sessions")
	if e != nil {
		return nil, e
	}
	out := []Session{}
	ds, e := os.ReadDir(root)
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	for _, d := range ds {
		if !d.IsDir() {
			continue
		}
		s, e := r.Session(ctx, endpoint, conversation, d.Name())
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (l *Ledger) SaveSession(s Session) error {
	p, e := SafePath(l.Root, s.EndpointID, s.ConversationID, "sessions", s.ID, "session.yaml")
	if e != nil {
		return e
	}
	s.UpdatedAt = time.Now().UnixMilli()
	return WriteYAML(p, s)
}
func (l *Ledger) AcquireSession(ctx context.Context, endpoint, conversation, agent, harness, revision string) (Session, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out Session
	workspace, e := SafePath(l.Root, endpoint, conversation, "workspace")
	if e != nil {
		return out, "", e
	}
	if e = os.MkdirAll(workspace, 0700); e != nil {
		return out, "", e
	}
	marker, e := SafePath(l.Root, endpoint, conversation, ".agentuse-workspace.json")
	if e != nil {
		return out, "", e
	}
	if _, e = os.Stat(marker); errors.Is(e, os.ErrNotExist) {
		e = writeJSON(marker, map[string]string{"endpoint_id": endpoint, "conversation_id": conversation})
		if e != nil {
			return out, "", e
		}
	}
	ss, e := l.Reader().Sessions(ctx, endpoint, conversation)
	if e != nil {
		return out, "", e
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].LastUsedAt > ss[j].LastUsedAt })
	for _, s := range ss {
		if s.AgentID == agent && s.Harness == harness && s.HarnessRevision == revision {
			if s.Status == "running" || s.Status == "pending_report" {
				return out, "", fmt.Errorf("session is busy")
			}
			if s.Status == "idle" {
				out = s
				break
			}
		}
	}
	now := time.Now().UnixMilli()
	if out.ID == "" {
		out = Session{ID: uuid.NewString(), EndpointID: endpoint, ConversationID: conversation, AgentID: agent, Harness: harness, HarnessRevision: revision, CreatedAt: now}
	}
	out.Status = "running"
	out.LastUsedAt = now
	if e = l.SaveSession(out); e != nil {
		return out, "", e
	}
	return out, workspace, nil
}
func (l *Ledger) ReleaseSession(s Session, contextSeq int64, valid bool, run ...string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s.LastContextSeq = max(s.LastContextSeq, contextSeq)
	s.Status = "idle"
	if !valid {
		s.Status = "unavailable"
	}
	if len(run) == 2 {
		p := l.paths[key(s.EndpointID, run[0], run[1])]
		var record Run
		if readRun(p, &record) == nil {
			if record.Status == "interrupted" {
				s.Status = "unavailable"
			} else if !record.ServerConfirmed && len(record.PendingReport) > 0 {
				s.Status = "pending_report"
			}
		}
	}
	if !valid {
		s.Status = "unavailable"
	}
	s.LastUsedAt = time.Now().UnixMilli()
	return l.SaveSession(s)
}
func (l *Ledger) RecoverSessions(ctx context.Context) error {
	endpoints, e := os.ReadDir(l.Root)
	if e != nil {
		return e
	}
	for _, ep := range endpoints {
		if !ep.IsDir() {
			continue
		}
		cs, e := l.Reader().Conversations(ctx, ep.Name())
		if e != nil {
			return e
		}
		for _, c := range cs {
			ss, e := l.Reader().Sessions(ctx, ep.Name(), c.ID)
			if e != nil {
				return e
			}
			for _, s := range ss {
				if s.Status == "running" {
					s.Status = "unavailable"
					if _, e := os.Stat(filepath.Join(l.Root, s.EndpointID, s.ConversationID, "sessions", s.ID, "events.jsonl")); e == nil {
						s.Status = "idle"
					}
					if e = l.SaveSession(s); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}

// Reclaim holds the same mutex as AcquireSession. Running sessions and pending
// results protect the entire conversation; shared workspaces are counted once.
func (l *Ledger) Reclaim(ctx context.Context, maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	type candidate struct {
		path       string
		size, last int64
	}
	var list []candidate
	var total int64
	endpoints, e := os.ReadDir(l.Root)
	if e != nil {
		return 0, e
	}
	for _, ep := range endpoints {
		if !ep.IsDir() {
			continue
		}
		cs, e := l.Reader().Conversations(ctx, ep.Name())
		if e != nil {
			return 0, e
		}
		for _, c := range cs {
			p, e := SafePath(l.Root, ep.Name(), c.ID)
			if e != nil {
				return 0, e
			}
			var marker struct {
				EndpointID     string `json:"endpoint_id"`
				ConversationID string `json:"conversation_id"`
			}
			b, e := os.ReadFile(filepath.Join(p, ".agentuse-workspace.json"))
			if e != nil {
				continue
			}
			if json.Unmarshal(b, &marker) != nil || marker.EndpointID != ep.Name() || marker.ConversationID != c.ID {
				continue
			}
			ss, e := l.Reader().Sessions(ctx, ep.Name(), c.ID)
			if e != nil {
				return 0, e
			}
			busy := l.reserved[ep.Name()+"/"+c.ID] > 0
			var last, size int64
			for _, s := range ss {
				if s.Status == "running" || s.Status == "pending_report" {
					busy = true
				}
				last = max(last, s.LastUsedAt)
			}
			e = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if d.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if !d.IsDir() {
					info, err := d.Info()
					if err != nil {
						return err
					}
					size += info.Size()
					if isRunRecord(l.Root, path) {
						var record Run
						if err = readRun(path, &record); err != nil {
							return err
						}
						if len(record.PendingReport) > 0 && !record.ServerConfirmed {
							busy = true
						}
					}
				}
				return nil
			})
			if e != nil {
				return 0, e
			}
			total += size
			if !busy {
				list = append(list, candidate{p, size, last})
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].last < list[j].last })
	for _, c := range list {
		if total <= maxBytes {
			break
		}
		if e = os.RemoveAll(c.path); e != nil {
			return total, e
		}
		total -= c.size
		for k, p := range l.paths {
			if rel, e := filepath.Rel(c.path, p); e == nil && rel != ".." && !stringsHasParent(rel) {
				delete(l.paths, k)
			}
		}
	}
	if total > maxBytes {
		return total, fmt.Errorf("workspaces exceed quota; all reclaimable directories are in use")
	}
	return total, nil
}
func stringsHasParent(s string) bool { return len(s) >= 3 && s[:3] == ".."+string(filepath.Separator) }

// Local conversations have their own reserved namespace. Only their title needs
// metadata; paths remain derived, with no endpoint registration or path registry.
func (l *Ledger) CreateLocalConversation(ctx context.Context, title string) (Conversation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return Conversation{}, e
	}
	if strings.TrimSpace(title) == "" || len(title) > 4096 {
		return Conversation{}, ErrInvalid
	}
	id := uuid.NewString()
	root, e := SafePath(l.Root, "local", id)
	if e != nil {
		return Conversation{}, e
	}
	if e = os.MkdirAll(filepath.Join(root, "workspace"), 0700); e != nil {
		return Conversation{}, e
	}
	if e = writeJSON(filepath.Join(root, ".agentuse-workspace.json"), map[string]string{"endpoint_id": "local", "conversation_id": id}); e != nil {
		return Conversation{}, e
	}
	if e = WriteYAML(filepath.Join(root, "conversation.yaml"), struct {
		Title string `yaml:"title"`
	}{title}); e != nil {
		return Conversation{}, e
	}
	return Conversation{ID: id, EndpointID: "local", Workspace: filepath.Join("local", id, "workspace"), Title: title}, nil
}
func (l *Ledger) LocalConversation(ctx context.Context, id string) (Conversation, error) {
	if e := ctx.Err(); e != nil {
		return Conversation{}, e
	}
	root, e := SafePath(l.Root, "local", id)
	if e != nil {
		return Conversation{}, e
	}
	var metadata struct {
		Title string `yaml:"title"`
	}
	if e = ReadYAML(filepath.Join(root, "conversation.yaml"), &metadata); e != nil {
		return Conversation{}, e
	}
	return Conversation{ID: id, EndpointID: "local", Workspace: filepath.Join("local", id, "workspace"), Title: metadata.Title}, nil
}

func (l *Ledger) HoldConversation(endpoint, conversation string) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	path, e := SafePath(l.Root, endpoint, conversation)
	if e != nil {
		return nil, e
	}
	if _, e = os.Stat(path); e != nil {
		return nil, e
	}
	if l.reserved == nil {
		l.reserved = map[string]int{}
	}
	key := endpoint + "/" + conversation
	l.reserved[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.reserved[key]--
			if l.reserved[key] == 0 {
				delete(l.reserved, key)
			}
		})
	}, nil
}
