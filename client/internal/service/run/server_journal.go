package run

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/memory-co/agentuse/client/internal/fileutil"
)

type runRecord struct {
	Type      string                     `json:"type"`
	At        int64                      `json:"at"`
	AttemptID string                     `json:"attempt_id"`
	Data      map[string]json.RawMessage `json:"data"`
}

func deliveryMetadata(r Delivery) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	json.Unmarshal(raw(r), &m)
	for _, key := range []string{"output", "usage", "cost_usd", "events", "directory"} {
		delete(m, key)
	}
	return m
}
func deliveryDiff(before, after map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for key, value := range after {
		if !bytes.Equal(before[key], value) {
			out[key] = value
		}
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			out[key] = json.RawMessage("null")
		}
	}
	return out
}
func appendDelivery(path, kind, attempt string, data map[string]json.RawMessage) error {
	if _, err := fileutil.SafePath(filepath.Dir(path), filepath.Base(path)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	// A crash may leave an incomplete final record. Never append onto that tail.
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		tail := []byte{0}
		if _, err = f.ReadAt(tail, info.Size()-1); err != nil {
			return err
		}
		if tail[0] != '\n' {
			b, err := io.ReadAll(f)
			if err != nil {
				return err
			}
			if err = f.Truncate(int64(bytes.LastIndexByte(b, '\n') + 1)); err != nil {
				return err
			}
		}
	}
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	record := runRecord{Type: kind, At: time.Now().UnixMilli(), AttemptID: attempt, Data: data}
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
func readDeliveryJournal(path string) ([]Delivery, error) {
	if _, err := fileutil.SafePath(filepath.Dir(path), filepath.Base(path)); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	states := map[string]map[string]json.RawMessage{}
	order := []string{}
	legacy := map[string]bool{}
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var record runRecord
		if err = json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		switch record.Type {
		case "received", "legacy_import":
			if record.Type == "legacy_import" {
				legacy[record.AttemptID] = true
			}
			if states[record.AttemptID] == nil {
				states[record.AttemptID] = map[string]json.RawMessage{}
				order = append(order, record.AttemptID)
			}
		case "updated":
			if states[record.AttemptID] == nil {
				return nil, fileutil.ErrInvalid
			}
		default:
			return nil, fileutil.ErrInvalid
		}
		for key, value := range record.Data {
			if bytes.Equal(value, []byte("null")) {
				delete(states[record.AttemptID], key)
			} else {
				states[record.AttemptID][key] = value
			}
		}
	}
	out := []Delivery{}
	for _, attempt := range order {
		var r Delivery
		if err = json.Unmarshal(raw(states[attempt]), &r); err != nil {
			return nil, err
		}
		if r.AttemptID != attempt {
			return nil, fileutil.ErrInvalid
		}
		r.legacy = legacy[attempt]
		out = append(out, r)
	}
	return out, nil
}
func readDeliveryMetadata(path string, r *Delivery) error {
	runs, err := readDeliveryJournal(path)
	if err != nil {
		return err
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if r.AttemptID == "" || runs[i].AttemptID == r.AttemptID {
			*r = runs[i]
			r.Directory = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))), "workspace")
			return nil
		}
	}
	return os.ErrNotExist
}
func readDelivery(path string, r *Delivery) error {
	if err := readDeliveryMetadata(path, r); err != nil {
		return err
	}
	if r.legacy {
		return nil
	}
	return projectDelivery(filepath.Join(filepath.Dir(filepath.Dir(path)), "events.jsonl"), r)
}
func resultPayload(r Delivery) json.RawMessage {
	return raw(struct {
		OK            bool     `json:"ok"`
		Output        string   `json:"output,omitempty"`
		Error         string   `json:"error,omitempty"`
		Usage         any      `json:"usage,omitempty"`
		CostUSD       *float64 `json:"cost_usd,omitempty"`
		HarnessRunIDs []string `json:"harness_run_ids,omitempty"`
	}{r.Status == "succeeded", r.Output, r.Error, r.Usage, r.CostUSD, r.HarnessRunIDs})
}
