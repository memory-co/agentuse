package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/memory-co/agentuse/client/internal/service/harness"
)

func (s *Service) Reconcile(ctx context.Context) error {
	if err := s.recoverRemoteReceipts(ctx); err != nil {
		return err
	}
	return s.rebuildNativeIndexes(ctx)
}
func (s *Service) pendingConversation(ctx context.Context, endpoint, conversation string) (bool, error) {
	dir := filepath.Join(s.Sessions.Root, endpoint, conversation)
	entries, err := os.ReadDir(filepath.Join(dir, ".server", "messages"))
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, e := os.ReadFile(filepath.Join(dir, ".server", "messages", entry.Name()))
		if e != nil {
			return false, e
		}
		var r messageReceipt
		if e = json.Unmarshal(data, &r); e != nil {
			return false, e
		}
		if r.State == "queued" || r.State == "preparing" || r.State == "submitting" || r.State == "unknown" {
			return true, nil
		}
	}
	sessions, err := s.Sessions.Sessions(ctx, endpoint, conversation)
	if err != nil {
		return false, err
	}
	for _, m := range sessions {
		root := filepath.Join(dir, "sessions", m.ID)
		controls, e := os.ReadDir(filepath.Join(root, ".server", "controls"))
		if e != nil && !os.IsNotExist(e) {
			return false, e
		}
		for _, entry := range controls {
			if entry.IsDir() {
				continue
			}
			raw, e := os.ReadFile(filepath.Join(root, ".server", "controls", entry.Name()))
			if e != nil {
				return false, e
			}
			var r controlReceipt
			if e = json.Unmarshal(raw, &r); e != nil {
				return false, e
			}
			if r.Status == "unknown" {
				return true, nil
			}
		}
		raw, e := os.ReadFile(filepath.Join(root, ".server", "stream.json"))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return false, e
		}
		var cursor streamReceipt
		if e = json.Unmarshal(raw, &cursor); e != nil {
			return false, e
		}
		source, e := os.ReadFile(filepath.Join(root, "events.jsonl"))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return false, e
		}
		if cursor.Offset < 0 || cursor.Offset > int64(len(source)) {
			return true, fmt.Errorf("invalid event cursor")
		}
		for _, line := range bytes.Split(source[cursor.Offset:], []byte{'\n'}) {
			var event struct {
				Type string `json:"type"`
			}
			if len(line) > 0 && (json.Unmarshal(line, &event) != nil || event.Type != "log") {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) Recover(ctx context.Context) error {
	endpoints, err := os.ReadDir(s.Sessions.Root)
	if err != nil {
		return err
	}
	var active []harness.RecoverySession
	for _, ep := range endpoints {
		if !ep.IsDir() {
			continue
		}
		conversations, err := s.Sessions.Conversations(ctx, ep.Name())
		if err != nil {
			return err
		}
		for _, c := range conversations {
			sessions, err := s.Sessions.Sessions(ctx, ep.Name(), c.ID)
			if err != nil {
				return err
			}
			for _, metadata := range sessions {
				if metadata.Status == "running" {
					active = append(active, harness.RecoverySession{Harness: metadata.Harness, Revision: metadata.HarnessRevision, EventsPath: filepath.Join(s.Sessions.Root, ep.Name(), c.ID, "sessions", metadata.ID, "events.jsonl")})
				}
			}
		}
	}
	return s.Harness.Recover(ctx, active)
}

// Rebuild receipt associations from durable public events, never by replaying send.
func (s *Service) recoverRemoteReceipts(ctx context.Context) error {
	endpoints, err := os.ReadDir(s.Sessions.Root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, ep := range endpoints {
		if !ep.IsDir() || ep.Name() == "local" {
			continue
		}
		conversations, err := s.Sessions.Conversations(ctx, ep.Name())
		if err != nil {
			return err
		}
		d := &endpointConnection{cfg: Config{EndpointID: ep.Name(), RunService: s}}
		for _, c := range conversations {
			receipts, err := d.receipts(c.ID)
			if err != nil {
				return err
			}
			bound := map[string]bool{}
			for _, r := range receipts {
				if r.State == "accepted" || r.State == "submitting" || r.State == "unknown" {
					if err = d.saveTombstone(r); err != nil {
						return err
					}
				}
				if r.InputID != "" {
					bound[r.SessionID+"/"+r.RunID+"/"+r.InputID] = true
				}
			}
			for _, r := range receipts {
				if r.State == "preparing" {
					r.State = "queued"
					if err = d.saveReceipt(r); err != nil {
						return err
					}
					continue
				}
				if r.State != "submitting" && r.State != "unknown" {
					continue
				}
				path := filepath.Join(s.Sessions.Root, ep.Name(), c.ID, "sessions", r.SessionID, "events.jsonl")
				source, e := os.ReadFile(path)
				if e != nil && !os.IsNotExist(e) {
					return e
				}
				if r.Offset < 0 || r.Offset > int64(len(source)) {
					r.State = "unknown"
					r.Error = "pending source position is unavailable"
					if e = d.saveReceipt(r); e != nil {
						return e
					}
					continue
				}
				complete := source[r.Offset:]
				if end := bytes.LastIndexByte(complete, '\n'); end >= 0 {
					complete = complete[:end+1]
				} else {
					complete = nil
				}
				var candidates []harness.Event
				for _, line := range bytes.Split(complete, []byte{'\n'}) {
					if len(line) == 0 {
						continue
					}
					var event harness.Event
					if e = json.Unmarshal(line, &event); e != nil {
						return e
					}
					if event.Type == "accepted" && !bound[r.SessionID+"/"+event.RunID+"/"+event.InputID] {
						candidates = append(candidates, event)
					}
				}
				if len(candidates) == 1 {
					r.State = "accepted"
					r.RunID = candidates[0].RunID
					r.InputID = candidates[0].InputID
					r.Error = ""
					bound[r.SessionID+"/"+r.RunID+"/"+r.InputID] = true
				} else {
					r.State = "unknown"
					r.Error = "input acceptance cannot be uniquely established; automatic replay stopped"
				}
				if e = d.saveReceipt(r); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
