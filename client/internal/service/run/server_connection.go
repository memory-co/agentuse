package run

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/version"
)

var errEndpointCredential = errors.New("设备密钥无效，请重新注册")

type endpointConnection struct {
	controlMu     sync.Mutex
	cfg           Config
	log           *log.Logger
	mu            sync.Mutex
	conn          *websocket.Conn
	active        map[int64]bool
	streamPending map[string]int64
	claimPending  bool
	groups        map[string]*sync.Mutex
}
type promptReceipt struct {
	Reported  bool            `json:"reported,omitempty"`
	Prompt    protocol.Prompt `json:"prompt"`
	SessionID string          `json:"session_id"`
	Offset    int64           `json:"offset"`
	State     string          `json:"state"`
	RunID     string          `json:"run_id,omitempty"`
	InputID   string          `json:"input_id,omitempty"`
	Error     string          `json:"error,omitempty"`
}
type streamReceipt struct {
	Offset int64 `json:"offset"`
}

func newConnection(cfg Config, logger *log.Logger) (*endpointConnection, error) {
	if cfg.Server == "" || cfg.Key == "" || cfg.RunService == nil {
		return nil, errors.New("registered endpoint and run service required")
	}
	if logger == nil {
		logger = log.Default()
	}
	return &endpointConnection{cfg: cfg, log: logger, active: map[int64]bool{}, streamPending: map[string]int64{}, groups: map[string]*sync.Mutex{}}, nil
}
func (d *endpointConnection) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := d.session(ctx); err != nil && ctx.Err() == nil {
			if errors.Is(err, errEndpointCredential) {
				return err
			}
			d.log.Printf("endpoint %s: %v", d.cfg.EndpointID, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
func (d *endpointConnection) send(ctx context.Context, m protocol.Message) error {
	d.mu.Lock()
	c := d.conn
	d.mu.Unlock()
	if c == nil {
		return errors.New("endpoint offline")
	}
	return wsjson.Write(ctx, c, &m)
}
func (d *endpointConnection) session(ctx context.Context) error {
	url := strings.Replace(strings.Replace(d.cfg.Server, "https://", "wss://", 1), "http://", "ws://", 1) + protocol.WSPath
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + d.cfg.Key}}})
	if err != nil {
		if response != nil && (response.StatusCode == 401 || response.StatusCode == 403) {
			return fmt.Errorf("%w: %v", errEndpointCredential, err)
		}
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(8 << 20)
	d.mu.Lock()
	d.conn = conn
	d.streamPending = map[string]int64{}
	d.claimPending = false
	d.mu.Unlock()
	defer func() { d.mu.Lock(); d.conn = nil; d.mu.Unlock() }()
	hs, err := d.cfg.Harness.Probe()
	if err != nil {
		return err
	}
	reported := []protocol.Harness{}
	for _, h := range hs {
		if !h.Disabled && h.Found {
			reported = append(reported, protocol.Harness{Name: h.Name, Version: h.Version, Revision: h.Revision})
		}
	}
	if err = d.send(ctx, protocol.Message{Type: protocol.TypeHello, Protocol: protocol.ExecutionProtocol, Version: version.Version, Harnesses: reported, Capabilities: []string{protocol.ExecutionProtocol, "session-attachments-v1"}}); err != nil {
		return err
	}
	var welcome protocol.Message
	if err = wsjson.Read(ctx, conn, &welcome); err != nil {
		return err
	}
	if welcome.Type != protocol.TypeWelcome || welcome.Protocol != protocol.ExecutionProtocol {
		return errors.New("server execution protocol upgrade required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	pollDone := make(chan struct{})
	defer func() { cancel(); <-pollDone }()
	errs := make(chan error, 1)
	go func() {
		defer close(pollDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		lastHeartbeat := time.Time{}
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				if err := d.flushEvents(runCtx); err != nil {
					select {
					case errs <- err:
					default:
					}
					conn.Close(websocket.StatusInternalError, "event synchronization failed")
					return
				}
				d.mu.Lock()
				claim := !d.claimPending && len(d.active) < cap(d.cfg.RunService.slots)
				if claim {
					d.claimPending = true
				}
				d.mu.Unlock()
				if claim {
					message := protocol.Message{Type: protocol.TypeClaim, ClaimID: "next", FreeSessions: cap(d.cfg.RunService.slots) - len(d.cfg.RunService.slots)}
					d.cfg.RunService.localMu.Lock()
					for _, active := range d.cfg.RunService.localRuns {
						if active.session.EndpointID == d.cfg.EndpointID {
							message.ActiveStreams = append(message.ActiveStreams, protocol.StreamRef{ConversationID: active.session.ConversationID, AgentID: active.session.AgentID, AgentVersionID: active.session.AgentVersionID})
						}
					}
					d.cfg.RunService.localMu.Unlock()
					if err := d.send(runCtx, message); err != nil {
						return
					}
				}
				if time.Since(lastHeartbeat) > 10*time.Second {
					lastHeartbeat = time.Now()
					if err := d.send(runCtx, protocol.Message{Type: protocol.TypeHeartbeat}); err != nil {
						return
					}
				}
			}
		}
	}()
	for {
		var m protocol.Message
		if err = wsjson.Read(ctx, conn, &m); err != nil {
			select {
			case e := <-errs:
				return e
			default:
				return err
			}
		}
		switch m.Type {
		case protocol.TypeNoTask:
			d.mu.Lock()
			d.claimPending = false
			d.mu.Unlock()
		case protocol.TypeReady:
		case protocol.TypePrompt:
			if m.Prompt == nil {
				return errors.New("missing prompt")
			}
			d.mu.Lock()
			d.claimPending = false
			busy := d.active[m.Prompt.ID]
			if !busy {
				d.active[m.Prompt.ID] = true
			}
			d.mu.Unlock()
			if !busy {
				prompt := *m.Prompt
				go d.receivePrompt(ctx, prompt)
			}
		case protocol.TypePromptErrorAck:
			if m.Prompt != nil {
				saved, found, e := d.findReceipt(ctx, *m.Prompt)
				if e != nil {
					return e
				}
				if found && saved.State == "failed" {
					saved.Reported = true
					metadata, e := d.cfg.RunService.Sessions.Session(ctx, d.cfg.EndpointID, m.Prompt.ConversationID, saved.SessionID)
					if e != nil {
						return e
					}
					if e = fileutil.WriteJSON(d.receiptPath(metadata, m.PromptID), saved); e != nil {
						return e
					}
				}
			}
		case protocol.TypeEventAck:
			if err = d.ackEvent(m.SessionID, m.AckSeq); err != nil {
				return err
			}
		case protocol.TypeCancel, protocol.TypeReply:
			if m.Control == nil {
				return errors.New("missing session control")
			}
			control := *m.Control
			go d.control(ctx, m.Type, control)
		case protocol.TypeError:
			return fmt.Errorf("server rejected event: %s", m.Error)
		default:
			return fmt.Errorf("unexpected server message %s", m.Type)
		}
	}
}
func (d *endpointConnection) sessionDir(metadata session.Session) string {
	return filepath.Join(d.cfg.RunService.Sessions.Root, metadata.EndpointID, metadata.ConversationID, "sessions", metadata.ID)
}
func (d *endpointConnection) receiptPath(metadata session.Session, id int64) string {
	return filepath.Join(d.sessionDir(metadata), ".server", "inputs", strconv.FormatInt(id, 10)+".json")
}
func (d *endpointConnection) findReceipt(ctx context.Context, p protocol.Prompt) (promptReceipt, bool, error) {
	sessions, err := d.cfg.RunService.Sessions.Sessions(ctx, d.cfg.EndpointID, p.ConversationID)
	if err != nil {
		return promptReceipt{}, false, err
	}
	for _, metadata := range sessions {
		path := d.receiptPath(metadata, p.ID)
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return promptReceipt{}, false, err
		}
		var saved promptReceipt
		if err = json.Unmarshal(raw, &saved); err != nil {
			return saved, false, err
		}
		return saved, true, nil
	}
	return promptReceipt{}, false, nil
}
func (d *endpointConnection) receivePrompt(ctx context.Context, p protocol.Prompt) {
	defer func() { d.mu.Lock(); delete(d.active, p.ID); d.mu.Unlock() }()
	d.mu.Lock()
	key := p.ConversationID + "/" + p.AgentID
	group := d.groups[key]
	if group == nil {
		group = &sync.Mutex{}
		d.groups[key] = group
	}
	d.mu.Unlock()
	group.Lock()
	defer group.Unlock()
	if saved, found, err := d.findReceipt(ctx, p); err != nil {
		d.log.Printf("prompt receipt: %v", err)
		return
	} else if found {
		if saved.State == "failed" {
			_ = d.send(ctx, protocol.Message{Type: protocol.TypePromptError, PromptID: p.ID, Prompt: &p, Error: saved.Error})
		}
		// A submitting receipt is uncertain after a crash. Never execute it again.
		return
	}
	var saved promptReceipt
	var path string
	request := Request{Harness: p.Harness, Prompt: p.Content, agentID: p.AgentID, agentVersionID: p.AgentVersionID, Model: p.Model, AgentInstructions: p.AgentInstructions, skills: p.Skills, environment: []string{"AGENTUSE_SERVER=" + d.cfg.Server, "AGENTUSE_TOKEN=" + d.cfg.Key, "AGENTUSE_CONFIG=" + d.cfg.ConfigPath, "AGENTUSE_WORK=" + p.WorkToken, "AGENTUSE_SESSION=" + p.ConversationID, "AGENTUSE_CONVERSATION=" + p.ConversationID, "AGENTUSE_AGENT=" + p.AgentID, "AGENTUSE_PROMPT=" + strconv.FormatInt(p.ID, 10), "AGENTUSE_RUN=", "AGENTUSE_ATTEMPT="}}
	if exe, err := os.Executable(); err == nil {
		request.environment = append(request.environment, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	request.environment = append(request.environment, "AGENTUSE_ENDPOINT="+d.cfg.EndpointID, "AGENTUSE_MESSAGE_ID="+strconv.FormatInt(p.MessageID, 10))

	request.beforeSend = func(metadata session.Session) error {
		path = d.receiptPath(metadata, p.ID)
		saved = promptReceipt{Prompt: p, SessionID: metadata.ID, State: "submitting"}
		events := filepath.Join(d.sessionDir(metadata), "events.jsonl")
		var offset int64
		if info, err := os.Stat(events); err == nil {
			offset = info.Size()
		} else if !os.IsNotExist(err) {
			return err
		}
		cursor := filepath.Join(d.sessionDir(metadata), ".server", "stream.json")
		if _, err := os.Stat(cursor); os.IsNotExist(err) {
			if err = fileutil.WriteJSON(cursor, streamReceipt{Offset: offset}); err != nil {
				return err
			}
		}
		cwd := filepath.Join(d.cfg.RunService.Sessions.Root, metadata.EndpointID, metadata.ConversationID, "workspace")
		if err := d.syncAttachments(ctx, &protocol.Prompt{ConversationID: p.ConversationID}, cwd); err != nil {
			return err
		}
		saved.Offset = offset
		return fileutil.WriteJSON(path, saved)
	}
	request.accepted = func(a harness.Acceptance) error {
		saved.State = "accepted"
		saved.RunID = a.RunID
		saved.InputID = a.InputID
		return fileutil.WriteJSON(path, saved)
	}
	_, err := d.cfg.RunService.startSession(ctx, d.cfg.EndpointID, p.ConversationID, request, "")
	if err != nil {
		if saved.State != "accepted" {
			saved.State = "failed"
			saved.Error = err.Error()
			if path != "" {
				_ = fileutil.WriteJSON(path, saved)
			}
			_ = d.send(ctx, protocol.Message{Type: protocol.TypePromptError, PromptID: p.ID, Prompt: &p, Error: err.Error()})
		}
	}
}
func (d *endpointConnection) streamMetadata(ctx context.Context, id string) (session.Session, error) {
	conversations, err := d.cfg.RunService.Sessions.Conversations(ctx, d.cfg.EndpointID)
	if err != nil {
		return session.Session{}, err
	}
	for _, c := range conversations {
		metadata, err := d.cfg.RunService.Sessions.Session(ctx, d.cfg.EndpointID, c.ID, id)
		if err == nil {
			return metadata, nil
		}
		if !os.IsNotExist(err) {
			return metadata, err
		}
	}
	return session.Session{}, os.ErrNotExist
}
func (d *endpointConnection) ackEvent(id string, offset int64) error {
	d.mu.Lock()
	expected := d.streamPending[id]
	if expected == 0 || offset != expected {
		d.mu.Unlock()
		return fmt.Errorf("unexpected event acknowledgement")
	}
	d.mu.Unlock()
	metadata, err := d.streamMetadata(context.Background(), id)
	if err != nil {
		return err
	}
	if err = fileutil.WriteJSON(filepath.Join(d.sessionDir(metadata), ".server", "stream.json"), streamReceipt{Offset: offset}); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.streamPending, id)
	d.mu.Unlock()
	return nil
}
func (d *endpointConnection) flushEvents(ctx context.Context) error {
	conversations, err := d.cfg.RunService.Sessions.Conversations(ctx, d.cfg.EndpointID)
	if err != nil {
		return err
	}
	for _, c := range conversations {
		sessions, err := d.cfg.RunService.Sessions.Sessions(ctx, d.cfg.EndpointID, c.ID)
		if err != nil {
			return err
		}
		for _, metadata := range sessions {
			dir := d.sessionDir(metadata)
			entries, e := os.ReadDir(filepath.Join(dir, ".server", "inputs"))
			if e != nil && !os.IsNotExist(e) {
				return e
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				data, e := os.ReadFile(filepath.Join(dir, ".server", "inputs", entry.Name()))
				if e != nil {
					return e
				}
				var saved promptReceipt
				if e = json.Unmarshal(data, &saved); e != nil {
					return e
				}
				if saved.State == "failed" && !saved.Reported {
					p := saved.Prompt
					if e = d.send(ctx, protocol.Message{Type: protocol.TypePromptError, PromptID: p.ID, Prompt: &p, Error: saved.Error}); e != nil {
						return e
					}
				}
			}
			d.mu.Lock()
			pending := d.streamPending[metadata.ID] != 0
			d.mu.Unlock()
			if pending {
				continue
			}
			cursorPath := filepath.Join(dir, ".server", "stream.json")
			var cursor streamReceipt
			data, err := os.ReadFile(cursorPath)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if err = json.Unmarshal(data, &cursor); err != nil {
				return err
			}
			file, err := os.Open(filepath.Join(dir, "events.jsonl"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			_, err = file.Seek(cursor.Offset, io.SeekStart)
			if err != nil {
				file.Close()
				return err
			}
			line, readErr := bufio.NewReader(file).ReadBytes('\n')
			file.Close()
			if readErr == io.EOF {
				continue
			}
			if readErr != nil {
				return readErr
			}
			envelope := protocol.ClientEvent{ConversationID: c.ID, AgentID: metadata.AgentID, AgentVersionID: metadata.AgentVersionID, SessionID: metadata.ID, Harness: metadata.Harness, Revision: metadata.HarnessRevision, Seq: cursor.Offset + int64(len(line)), Event: json.RawMessage(strings.TrimSuffix(string(line), "\n"))}
			var event harness.Event
			if err = json.Unmarshal(line, &event); err != nil {
				return err
			}
			if event.Type == "accepted" {
				entries, err := os.ReadDir(filepath.Join(dir, ".server", "inputs"))
				if err != nil {
					return err
				}
				for _, entry := range entries {
					raw, err := os.ReadFile(filepath.Join(dir, ".server", "inputs", entry.Name()))
					if err != nil {
						return err
					}
					var receipt promptReceipt
					if err = json.Unmarshal(raw, &receipt); err != nil {
						return err
					}
					if receipt.InputID == event.InputID && receipt.RunID == event.RunID {
						envelope.PromptID = receipt.Prompt.ID
						break
					}
				}
				if envelope.PromptID == 0 {
					continue
				}
			}
			d.mu.Lock()
			d.streamPending[metadata.ID] = envelope.Seq
			d.mu.Unlock()
			if err = d.send(ctx, protocol.Message{Type: protocol.TypeEvent, ClientEvent: &envelope}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (d *endpointConnection) control(ctx context.Context, kind string, c protocol.SessionControl) {
	d.controlMu.Lock()
	defer d.controlMu.Unlock()
	if !fileutil.Segment(c.ID) {
		return
	}
	metadata, err := d.cfg.RunService.Sessions.Session(ctx, d.cfg.EndpointID, c.ConversationID, c.SessionID)
	if err != nil {
		_ = d.send(ctx, protocol.Message{Type: protocol.TypeControlAck, Control: &c, Error: err.Error()})
		return
	}
	receipt := filepath.Join(d.sessionDir(metadata), ".server", "controls", c.ID+".json")
	var saved struct {
		Error string `json:"error"`
	}
	if data, err := os.ReadFile(receipt); err == nil {
		if json.Unmarshal(data, &saved) == nil {
			_ = d.send(ctx, protocol.Message{Type: protocol.TypeControlAck, Control: &c, Error: saved.Error})
			return
		}
	}
	body := json.RawMessage(`{}`)
	action := "cancel"
	if kind == protocol.TypeReply {
		action = "reply"
		body, _ = json.Marshal(map[string]any{"request_id": c.RequestID, "answer": c.Answer})
	}
	// A replayed cancellation must never cancel a later native interaction.
	native, lookupErr := d.cfg.RunService.Detail(ctx, d.cfg.EndpointID, c.ConversationID, c.SessionID, c.RunID)
	if lookupErr != nil {
		err = lookupErr
	} else if native.Status == "completed" || native.Status == "failed" || native.Status == "cancelled" {
		err = nil
	} else {
		_, err = d.cfg.RunService.controlSession(ctx, d.cfg.EndpointID, c.ConversationID, c.SessionID, action, body)
	}
	if err != nil {
		saved.Error = err.Error()
	}
	if e := fileutil.WriteJSON(receipt, saved); e != nil {
		saved.Error = e.Error()
	}
	_ = d.send(ctx, protocol.Message{Type: protocol.TypeControlAck, Control: &c, Error: saved.Error})
}
