package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/internal/protocol"
)

type controlReceipt struct {
	Control protocol.SessionControl `json:"control"`
	Kind    string                  `json:"kind"`
	Status  string                  `json:"status"`
	Error   string                  `json:"error,omitempty"`
}

func (d *endpointConnection) control(ctx context.Context, kind string, c protocol.SessionControl) {
	d.controlMu.Lock()
	defer d.controlMu.Unlock()
	reply := func(status, message string) {
		_ = d.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: c.ID, Status: status, Error: message})
	}
	if !fileutil.Segment(c.ID) {
		reply(protocol.AckError, "invalid control id")
		return
	}
	m, err := d.streamMetadata(ctx, c.SessionID)
	if err != nil {
		reply(protocol.AckError, err.Error())
		return
	}
	if c.ConversationID != "" && c.ConversationID != m.ConversationID {
		reply(protocol.AckError, "control route conflict")
		return
	}
	c.ConversationID = m.ConversationID
	path := filepath.Join(d.sessionDir(m), ".server", "controls", receiptName(c.ID))
	saved := controlReceipt{Control: c, Kind: kind, Status: protocol.AckUnknown}
	if data, e := os.ReadFile(path); e == nil {
		if e = json.Unmarshal(data, &saved); e != nil {
			reply(protocol.AckUnknown, e.Error())
			return
		}
		original, _ := json.Marshal(saved.Control)
		current, _ := json.Marshal(c)
		if saved.Kind != kind || string(original) != string(current) {
			reply(protocol.AckError, "control identity content conflict")
			return
		}
		reply(saved.Status, saved.Error)
		return
	} else if !os.IsNotExist(e) {
		reply(protocol.AckUnknown, e.Error())
		return
	}
	native, err := d.cfg.RunService.Detail(ctx, d.cfg.EndpointID, c.ConversationID, c.SessionID, c.RunID)
	if err != nil {
		reply(protocol.AckError, err.Error())
		return
	}
	if native.Status == "completed" || native.Status == "failed" || native.Status == "cancelled" {
		saved.Status = protocol.AckOK
	} else {
		if kind == protocol.TypeCancel {
			status, e := d.cfg.RunService.SessionStatus(ctx, d.cfg.EndpointID, c.ConversationID, c.SessionID)
			data, _ := json.Marshal(status)
			var active struct {
				RunID string `json:"run_id"`
			}
			_ = json.Unmarshal(data, &active)
			if e != nil || active.RunID != c.RunID {
				reply(protocol.AckUnknown, "cannot confirm cancellation target")
				return
			}
		} else if c.RequestID == "" || !json.Valid(c.Answer) {
			reply(protocol.AckError, "request and answer required")
			return
		}
		// Save uncertainty before crossing the native side-effect boundary.
		saved.Error = "control may already be applied; automatic replay stopped"
		if err = fileutil.WriteJSON(path, saved); err != nil {
			reply(protocol.AckUnknown, err.Error())
			return
		}
		action := "cancel"
		body := json.RawMessage(`{}`)
		if kind == protocol.TypeAnswer {
			action = "reply"
			body, _ = json.Marshal(map[string]any{"request_id": c.RequestID, "answer": c.Answer})
		}
		_, err = d.cfg.RunService.controlSession(ctx, d.cfg.EndpointID, c.ConversationID, c.SessionID, action, body)
		if err != nil {
			saved.Status = protocol.AckUnknown
			var exit *exec.ExitError
			if errors.As(err, &exit) && (exit.ExitCode() == 2 || exit.ExitCode() == 4) {
				saved.Status = protocol.AckError
			}
			saved.Error = err.Error()
		} else {
			saved.Status = protocol.AckOK
			saved.Error = ""
		}
	}
	if err = fileutil.WriteJSON(path, saved); err != nil {
		reply(protocol.AckUnknown, fmt.Sprint(err))
		return
	}
	reply(saved.Status, saved.Error)
}
