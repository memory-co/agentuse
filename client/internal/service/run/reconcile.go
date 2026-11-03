package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/harness"
)

func (s *Service) Reconcile(ctx context.Context) error {
	if err := s.recoverRemoteReceipts(ctx); err != nil {
		return err
	}
	return s.rebuildNativeIndexes(ctx)
}
func (s *Service) pendingConversation(ctx context.Context, endpoint, conversation string) (bool, error) {
	sessions, err := s.Sessions.Sessions(ctx, endpoint, conversation)
	if err != nil {
		return false, err
	}
	for _, metadata := range sessions {
		dir := filepath.Join(s.Sessions.Root, endpoint, conversation, "sessions", metadata.ID)
		entries, e := os.ReadDir(filepath.Join(dir, ".server", "inputs"))
		if e != nil && !os.IsNotExist(e) {
			return false, e
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, e := os.ReadFile(filepath.Join(dir, ".server", "inputs", entry.Name()))
			if e != nil {
				return false, e
			}
			var receipt promptReceipt
			if e = json.Unmarshal(data, &receipt); e != nil {
				return false, e
			}
			if receipt.State == "submitting" || (receipt.State == "failed" && !receipt.Reported) {
				return true, nil
			}
		}
		cursorData, err := os.ReadFile(filepath.Join(dir, ".server", "stream.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		var cursor streamReceipt
		if err = json.Unmarshal(cursorData, &cursor); err != nil {
			return false, err
		}
		info, err := os.Stat(filepath.Join(dir, "events.jsonl"))
		if err == nil && info.Size() > cursor.Offset {
			return true, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return false, err
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

// Receipts never cause execution replay. After adapter recovery has cancelled
// foreground work, recover bindings from the authoritative accepted records.
func (s *Service) recoverRemoteReceipts(ctx context.Context) error {
	endpoints, err := os.ReadDir(s.Sessions.Root)
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
		for _, c := range conversations {
			sessions, err := s.Sessions.Sessions(ctx, ep.Name(), c.ID)
			if err != nil {
				return err
			}
			for _, metadata := range sessions {
				dir := filepath.Join(s.Sessions.Root, ep.Name(), c.ID, "sessions", metadata.ID)
				inputs := filepath.Join(dir, ".server", "inputs")
				entries, err := os.ReadDir(inputs)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				source, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				for _, entry := range entries {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
						continue
					}
					path := filepath.Join(inputs, entry.Name())
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					var receipt promptReceipt
					if err = json.Unmarshal(data, &receipt); err != nil {
						return err
					}
					if receipt.State != "submitting" {
						continue
					}
					if receipt.Offset < 0 || receipt.Offset > int64(len(source)) {
						return fmt.Errorf("invalid pending receipt offset")
					}
					candidates := []harness.Event{}
					complete := source[receipt.Offset:]
					if end := bytes.LastIndexByte(complete, '\n'); end >= 0 {
						complete = complete[:end+1]
					} else {
						complete = nil
					}
					for _, line := range bytes.Split(complete, []byte{'\n'}) {
						if len(line) == 0 {
							continue
						}
						var event harness.Event
						if err = json.Unmarshal(line, &event); err != nil {
							return err
						}
						if event.Type == "accepted" && event.Text == receipt.Prompt.Content {
							candidates = append(candidates, event)
						}
					}
					if len(candidates) > 1 {
						return fmt.Errorf("ambiguous accepted input for prompt %d", receipt.Prompt.ID)
					}
					if len(candidates) == 1 {
						receipt.State = "accepted"
						receipt.RunID = candidates[0].RunID
						receipt.InputID = candidates[0].InputID
					} else {
						receipt.State = "failed"
						receipt.Error = "client restarted before input acceptance; executor recovery confirmed cancellation"
					}
					if err = fileutil.WriteJSON(path, receipt); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
