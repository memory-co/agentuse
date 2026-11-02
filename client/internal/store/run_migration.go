package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
)

// migrateRuns converts the previous layout once, keeping adapter-owned files
// untouched. Pre-harnessctl history is preserved once as legacy_import, since
// no authoritative adapter event file exists for those old executions.
func (l *Ledger) migrateLegacyHierarchy(ctx context.Context) error {
	return filepath.WalkDir(l.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(l.Root, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if d.IsDir() {
			if len(parts) == 3 && parts[2] == "workspace" {
				return filepath.SkipDir
			}
			if len(parts) == 5 && parts[4] == "events.jsonl.d" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(parts) != 8 || parts[2] != "sessions" || parts[4] != "runs" || parts[7] != "run.json" {
			return nil
		}
		if _, err = SafePath(l.Root, parts...); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var old Delivery
		if err = json.Unmarshal(data, &old); err != nil {
			return err
		}
		if old.EndpointID != parts[0] || old.ConversationID != parts[1] || old.SessionID != parts[3] || old.RunID != parts[5] || old.AttemptID != parts[6] {
			return fmt.Errorf("legacy run identity mismatch: %s", rel)
		}
		legacy := len(old.HarnessRunIDs) == 0 && (old.Output != "" || len(old.Events) > 0 || old.Usage != nil)
		if len(old.HarnessRunIDs) > 0 {
			projected := old
			projected.Output = ""
			projected.Usage = nil
			projected.CostUSD = nil
			projected.Events = nil
			events := filepath.Join(l.Root, old.EndpointID, old.ConversationID, "sessions", old.SessionID, "events.jsonl")
			// If its source no longer exists, preserve the only surviving result.
			if e := projectDelivery(events, &projected); e != nil || old.Output != "" && old.Output != projected.Output {
				legacy = true
			}
		}
		if len(old.PendingReport) > 0 {
			var report map[string]json.RawMessage
			if err = json.Unmarshal(old.PendingReport, &report); err != nil {
				return err
			}
			delete(report, "payload")
			old.PendingReport = raw(report)
		}
		target := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(path))), old.RunID+".jsonl")
		current := Delivery{AttemptID: old.AttemptID}
		if err = readDeliveryMetadata(target, &current); os.IsNotExist(err) {
			kind := "received"
			data := deliveryMetadata(old)
			if legacy {
				kind = "legacy_import"
				json.Unmarshal(raw(old), &data)
				if input, e := os.ReadFile(filepath.Join(filepath.Dir(path), "input.json")); e == nil && json.Valid(input) {
					data["legacy_input"] = input
				}
			}
			if err = appendDelivery(target, kind, old.AttemptID, data); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if legacy && !current.legacy {
			// Resume a partially completed migration without dropping the only
			// surviving result or overwriting newer delivery acknowledgments.
			archive := map[string]json.RawMessage{"output": raw(old.Output), "usage": raw(old.Usage), "cost_usd": raw(old.CostUSD), "events": raw(old.Events)}
			if err = appendDelivery(target, "legacy_import", old.AttemptID, archive); err != nil {
				return err
			}
		}
		history := filepath.Join(filepath.Dir(path), "history.json")
		if b, e := os.ReadFile(history); e == nil && strings.TrimSpace(string(b)) != "[]" {
			resource := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))), "resources", old.RunID+"."+old.AttemptID+".history.json")
			if err = Atomic(resource, b); err != nil {
				return err
			}
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
		for _, name := range []string{"run.json", "input.json", "history.json"} {
			if err = os.Remove(filepath.Join(filepath.Dir(path), name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		// Remove empty directories only; never delete unknown user files.
		_ = os.Remove(filepath.Dir(path))
		_ = os.Remove(filepath.Dir(filepath.Dir(path)))
		return nil
	})
}

// Move old caller-owned records out of runs/. Only accepted adapter UUIDs
// remain in the public run index. Server wire state is deliberately separate.
func (l *Ledger) migrateRuns(ctx context.Context) error {
	if err := l.migrateLegacyHierarchy(ctx); err != nil {
		return err
	}
	return filepath.WalkDir(l.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(l.Root, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if d.IsDir() {
			if len(parts) == 3 && parts[2] == "workspace" || len(parts) == 5 && parts[4] == "events.jsonl.d" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(parts) == 5 && parts[2] == "sessions" && parts[4] == "session.yaml" {
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			var legacy struct {
				Seq int64 `yaml:"last_context_seq"`
			}
			if e = yaml.Unmarshal(data, &legacy); e != nil {
				return e
			}
			cursor := filepath.Join(filepath.Dir(path), ".server", "context.json")
			if _, e = os.Stat(cursor); os.IsNotExist(e) && legacy.Seq > 0 {
				if e = Atomic(cursor, raw(map[string]int64{"message_seq": legacy.Seq})); e != nil {
					return e
				}
			}
			return nil
		}
		if len(parts) != 6 || parts[2] != "sessions" || !strings.HasSuffix(parts[5], ".jsonl") || (parts[4] != "runs" && parts[4] != ".server" && parts[4] != "legacy") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var header struct{ Type string }
		line, _, _ := strings.Cut(string(data), "\n")
		if err = json.Unmarshal([]byte(line), &header); err != nil {
			return err
		}
		if header.Type == "reference" {
			return nil
		}
		records, err := readDeliveryJournal(path)
		if err != nil {
			return err
		}
		if parts[4] == "runs" {
			folder := ".server"
			if parts[0] == "local" {
				folder = "legacy"
			}
			target := filepath.Join(filepath.Dir(filepath.Dir(path)), folder, parts[5])
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			if err = os.Rename(path, target); err != nil {
				return err
			}
		}
		for _, record := range records {
			session := Session{ID: record.SessionID, EndpointID: record.EndpointID, ConversationID: record.ConversationID, AgentID: record.AgentID, Harness: record.Harness, HarnessRevision: record.HarnessRevision}
			for _, id := range record.HarnessRunIDs {
				if parsed, e := uuid.Parse(id); e != nil || parsed.String() != id {
					continue
				}
				if err = l.AcceptNative(ctx, session, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
