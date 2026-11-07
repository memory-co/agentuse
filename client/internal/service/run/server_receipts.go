package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/internal/protocol"
)

// A small endpoint-level receipt outlives quota reclamation of a conversation.
// It prevents a delayed duplicate from creating a fresh native session after
// its public event file has already been collected.
type messageTombstone struct {
	Digest  string `json:"digest"`
	State   string `json:"state"`
	RunID   string `json:"run_id,omitempty"`
	InputID string `json:"input_id,omitempty"`
}

func (d *endpointConnection) tombstonePath(p protocol.ConversationMessage) string {
	return filepath.Join(d.cfg.RunService.Sessions.Root, d.cfg.EndpointID, ".server", "messages", receiptName(p.Key()))
}
func messageDigest(p protocol.ConversationMessage) string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (d *endpointConnection) saveTombstone(r messageReceipt) error {
	return fileutil.WriteJSON(d.tombstonePath(r.Message), messageTombstone{Digest: messageDigest(r.Message), State: r.State, RunID: r.RunID, InputID: r.InputID})
}
func (d *endpointConnection) readTombstone(p protocol.ConversationMessage) (messageReceipt, bool, error) {
	raw, err := os.ReadFile(d.tombstonePath(p))
	if os.IsNotExist(err) {
		return messageReceipt{}, false, nil
	}
	if err != nil {
		return messageReceipt{}, false, err
	}
	var t messageTombstone
	if err = json.Unmarshal(raw, &t); err != nil {
		return messageReceipt{}, false, err
	}
	if t.Digest != messageDigest(p) {
		return messageReceipt{}, false, fmt.Errorf("message identity conflicts with retained receipt")
	}
	return messageReceipt{Message: p, State: "unknown", RunID: t.RunID, InputID: t.InputID, Error: "native input was previously submitted; local event history is no longer available"}, true, nil
}

// Recover a late accepted after the original send returned an uncertain result.
func (d *endpointConnection) lateAcceptance(ctx context.Context, conversation, sessionID string, eventRun, eventInput string, pos int64, receipts []messageReceipt) (string, error) {
	var candidates []messageReceipt
	bound := map[string]bool{}
	for _, r := range receipts {
		if r.SessionID == sessionID {
			if r.RunID != "" && r.InputID != "" {
				bound[r.RunID+"/"+r.InputID] = true
			}
			if r.State == "unknown" && r.Offset < pos {
				candidates = append(candidates, r)
			}
		}
	}
	if len(candidates) != 1 {
		return "", nil
	}
	r := candidates[0]
	m, err := d.cfg.RunService.Sessions.Session(ctx, d.cfg.EndpointID, conversation, sessionID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(d.sessionDir(m), "events.jsonl"))
	if err != nil {
		return "", err
	}
	if r.Offset < 0 || r.Offset > int64(len(data)) {
		return "", fmt.Errorf("invalid pending event position")
	}
	remaining := data[r.Offset:]
	var uniqueRun, uniqueInput string
	count := 0
	for _, line := range completeEventLines(remaining) {
		var event struct {
			Type  string `json:"type"`
			Run   string `json:"run_id"`
			Input string `json:"input_id"`
		}
		if err = json.Unmarshal(line, &event); err != nil {
			return "", err
		}
		if event.Type == "accepted" && !bound[event.Run+"/"+event.Input] {
			count++
			uniqueRun = event.Run
			uniqueInput = event.Input
		}
	}
	if count != 1 || uniqueRun != eventRun || uniqueInput != eventInput {
		return "", nil
	}
	r.State = "accepted"
	r.RunID = uniqueRun
	r.InputID = uniqueInput
	r.Error = ""
	if err = d.saveReceipt(r); err != nil {
		return "", err
	}
	if err = d.saveTombstone(r); err != nil {
		return "", err
	}
	return r.Message.MessageID, nil
}
func completeEventLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			if i > start {
				out = append(out, raw[start:i])
			}
			start = i + 1
		}
	}
	return out
}
