package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

// Execution content is read from the adapter's public JSONL, never copied into
// the client journal. Adapter private state remains entirely opaque.
func projectDelivery(path string, r *Delivery) error {
	if len(r.HarnessRunIDs) == 0 {
		return nil
	}
	if _, err := SafePath(filepath.Dir(path), filepath.Base(path)); err != nil {
		return err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err != nil {
		return err
	}
	defer file.Close()
	owned := map[string]bool{}
	for _, id := range r.HarnessRunIDs {
		owned[id] = true
	}
	results := map[string]bool{}
	pending := map[string]bool{}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		var event struct {
			Type, Text, State, Kind, Message, Status, Output string
			RunID                                            string `json:"run_id"`
			RequestID                                        string `json:"request_id"`
			TS                                               string `json:"ts"`
			Questions                                        []struct {
				ID, Text, Header string
				Multiple         bool
				Options          []protocol.Option
			} `json:"questions"`
			Usage   *sdk.Usage `json:"usage"`
			CostUSD *float64   `json:"cost_usd"`
			Error   *string    `json:"error"`
		}
		if err = json.Unmarshal(line, &event); err != nil {
			return err
		}
		if !owned[event.RunID] {
			continue
		}
		at := r.ReceivedAt
		if stamp, e := time.Parse(time.RFC3339Nano, event.TS); e == nil {
			at = stamp.UnixMilli()
		}
		kind := event.Type
		payload := json.RawMessage(line)
		switch event.Type {
		case "message":
			kind = "progress"
			payload = raw(map[string]string{"text": event.Text, "harness_run_id": event.RunID})
		case "request":
			switch event.State {
			case "pending":
				kind = "question"
				pending[event.RequestID] = true
			case "replied":
				kind = "answered"
				delete(pending, event.RequestID)
			case "expired":
				kind = "question_expired"
				delete(pending, event.RequestID)
			}
			q := protocol.QuestionPayload{ToolUseID: event.RequestID}
			if event.Kind == "permission" {
				q.Questions = []protocol.Question{{Question: event.Message, Options: []protocol.Option{{Label: "allow"}, {Label: "deny"}}}}
			} else {
				for _, item := range event.Questions {
					q.Questions = append(q.Questions, protocol.Question{Question: item.Text, Header: item.Header, MultiSelect: item.Multiple, Options: item.Options})
				}
			}
			payload = raw(q)
		case "result":
			if results[event.RunID] {
				continue
			}
			results[event.RunID] = true
			if event.Status == "completed" {
				kind = "done"
			} else {
				kind = "failed"
			}
			if r.Output != "" && event.Output != "" {
				r.Output += "\n\n"
			}
			r.Output += event.Output
			if event.Error != nil && r.Error == "" {
				r.Error = *event.Error
			}
			if event.Usage != nil {
				if r.Usage == nil {
					r.Usage = &sdk.Usage{}
				}
				r.Usage.InputTokens += event.Usage.InputTokens
				r.Usage.OutputTokens += event.Usage.OutputTokens
				r.Usage.CacheReadTokens += event.Usage.CacheReadTokens
				r.Usage.CacheWriteTokens += event.Usage.CacheWriteTokens
			}
			if event.CostUSD != nil {
				if r.CostUSD == nil {
					r.CostUSD = new(float64)
				}
				*r.CostUSD += *event.CostUSD
			}
		}
		r.Events = append(r.Events, Event{Kind: kind, At: at, Payload: payload})
		if len(r.Events) > 200 {
			r.Events = r.Events[len(r.Events)-200:]
		}
	}
	if r.Status == "running" || r.Status == "waiting" {
		if len(pending) > 0 {
			r.Status = "waiting"
		} else {
			r.Status = "running"
		}
	}
	return nil
}

// Recover only the unbound tail of an interrupted execution, before new work
// can enter this session. A saved offset prevents claiming earlier inputs.
func recoverBindings(path string, offset int64) ([]string, error) {
	if _, err := SafePath(filepath.Dir(path), filepath.Base(path)); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(f)
	if offset > 0 {
		previous := []byte{0}
		if _, err = f.ReadAt(previous, offset-1); err != nil {
			return nil, err
		}
		if previous[0] != '\n' {
			if _, err = reader.ReadBytes('\n'); err != nil && err != io.EOF {
				return nil, err
			}
		}
	}
	ids := []string{}
	seen := map[string]bool{}
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return ids, nil
		}
		if err != nil {
			return nil, err
		}
		var event struct {
			Type  string
			RunID string `json:"run_id"`
		}
		if err = json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		if event.Type == "accepted" && !seen[event.RunID] {
			seen[event.RunID] = true
			ids = append(ids, event.RunID)
		}
	}
}
