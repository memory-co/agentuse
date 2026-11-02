package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// migrateRuns converts the previous layout once, keeping adapter-owned files
// untouched. Pre-harnessctl history is preserved once as legacy_import, since
// no authoritative adapter event file exists for those old executions.
func (l *Ledger) migrateRuns(ctx context.Context) error {
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
		var old Run
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
			if e := projectHarness(events, &projected); e != nil || old.Output != "" && old.Output != projected.Output {
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
		current := Run{AttemptID: old.AttemptID}
		if err = readRunMetadata(target, &current); os.IsNotExist(err) {
			kind := "received"
			data := metadata(old)
			if legacy {
				kind = "legacy_import"
				json.Unmarshal(raw(old), &data)
				if input, e := os.ReadFile(filepath.Join(filepath.Dir(path), "input.json")); e == nil && json.Valid(input) {
					data["legacy_input"] = input
				}
			}
			if err = appendRun(target, kind, old.AttemptID, data); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if legacy && !current.legacy {
			// Resume a partially completed migration without dropping the only
			// surviving result or overwriting newer delivery acknowledgments.
			archive := map[string]json.RawMessage{"output": raw(old.Output), "usage": raw(old.Usage), "cost_usd": raw(old.CostUSD), "events": raw(old.Events)}
			if err = appendRun(target, "legacy_import", old.AttemptID, archive); err != nil {
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
