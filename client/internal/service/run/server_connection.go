package run

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/internal/protocol"
)

var errEndpointCredential = errors.New("设备密钥无效，请重新注册")
var errEndpointProtocol = errors.New("client/server protocol upgrade required")

type endpointConnection struct {
	cfg           Config
	log           *log.Logger
	mu            sync.Mutex
	writeMu       sync.Mutex
	controlMu     sync.Mutex
	conn          *websocket.Conn
	active        map[string]bool
	queues        map[string][]protocol.ConversationMessage
	streamPending map[string]int64
	replayPending map[string]bool
	harnesses     []protocol.Harness
}
type messageReceipt struct {
	Message   protocol.ConversationMessage `json:"message"`
	SessionID string                       `json:"session_id,omitempty"`
	Offset    int64                        `json:"offset"`
	State     string                       `json:"state"`
	RunID     string                       `json:"run_id,omitempty"`
	InputID   string                       `json:"input_id,omitempty"`
	Error     string                       `json:"error,omitempty"`
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
	return &endpointConnection{cfg: cfg, log: logger, active: map[string]bool{}, queues: map[string][]protocol.ConversationMessage{}, streamPending: map[string]int64{}, replayPending: map[string]bool{}}, nil
}
func (d *endpointConnection) Run(ctx context.Context) error {
	hs, err := d.cfg.Harness.Probe()
	if err != nil {
		return err
	}
	for _, h := range hs {
		if !h.Disabled && h.Found {
			d.harnesses = append(d.harnesses, protocol.Harness{Name: h.Name, Version: h.Version, Revision: h.Revision})
		}
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := d.session(ctx); err != nil && ctx.Err() == nil {
			if errors.Is(err, errEndpointCredential) || errors.Is(err, errEndpointProtocol) {
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
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	d.mu.Lock()
	c := d.conn
	d.mu.Unlock()
	if c == nil {
		return errors.New("endpoint offline")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c, m)
}
func (d *endpointConnection) heartbeat() protocol.Message {
	m := protocol.Message{Type: protocol.TypeHeartbeat, ID: "h:" + uuid.NewString(), FreeSessions: cap(d.cfg.RunService.slots) - len(d.cfg.RunService.slots), Harnesses: d.harnesses, ActiveStreams: []protocol.StreamRef{}}
	d.cfg.RunService.localMu.Lock()
	defer d.cfg.RunService.localMu.Unlock()
	for _, a := range d.cfg.RunService.localRuns {
		if a.session.EndpointID == d.cfg.EndpointID {
			m.ActiveStreams = append(m.ActiveStreams, protocol.StreamRef{ConversationID: a.session.ConversationID, AgentID: a.session.AgentID, AgentVersionID: a.session.AgentVersionID})
		}
	}
	sort.Slice(m.ActiveStreams, func(i, j int) bool {
		a, b := m.ActiveStreams[i], m.ActiveStreams[j]
		return a.ConversationID+"/"+a.AgentID+"/"+a.AgentVersionID < b.ConversationID+"/"+b.AgentID+"/"+b.AgentVersionID
	})
	return m
}
func (d *endpointConnection) session(ctx context.Context) error {
	url := strings.Replace(strings.Replace(d.cfg.Server, "https://", "wss://", 1), "http://", "ws://", 1) + protocol.WSPath
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + d.cfg.Key}, protocol.ProtocolHeader: []string{protocol.ExecutionProtocol}}})
	if err != nil {
		if response != nil {
			if response.StatusCode == 401 || response.StatusCode == 403 {
				return fmt.Errorf("%w: %v", errEndpointCredential, err)
			}
			if response.StatusCode == 426 {
				return errEndpointProtocol
			}
		}
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(8 << 20)
	d.mu.Lock()
	d.conn = conn
	d.streamPending = map[string]int64{}
	d.replayPending = map[string]bool{}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		if d.conn == conn {
			d.conn = nil
		}
		d.mu.Unlock()
	}()
	if err = d.send(ctx, d.heartbeat()); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		last := time.Now()
		previous := ""
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				if err := d.flushEvents(runCtx); err != nil {
					d.log.Printf("event synchronization: %v", err)
					conn.Close(websocket.StatusInternalError, "event synchronization failed")
					return
				}
				m := d.heartbeat()
				m.ID = ""
				raw, _ := json.Marshal(m)
				if string(raw) != previous || time.Since(last) > 10*time.Second {
					m.ID = "h:" + uuid.NewString()
					if d.send(runCtx, m) != nil {
						return
					}
					previous = string(raw)
					last = time.Now()
				}
			}
		}
	}()
	for {
		var m protocol.Message
		if err = wsjson.Read(ctx, conn, &m); err != nil {
			return err
		}
		switch m.Type {
		case protocol.TypeMessage:
			if err = d.enqueue(*m.Delivery); err != nil {
				return err
			}
		case protocol.TypeAck:
			if strings.HasPrefix(m.ID, "h:") {
				if m.Status != protocol.AckOK {
					return fmt.Errorf("heartbeat rejected: %s", m.Error)
				}
				continue
			}
			if !strings.HasPrefix(m.ID, "e:") {
				return fmt.Errorf("unexpected ACK %s", m.ID)
			}
			if m.Status != protocol.AckOK {
				return fmt.Errorf("server rejected event %s: %s", m.ID, m.Error)
			}
			if err = d.ackEventID(m.ID); err != nil {
				return err
			}
		case protocol.TypeAnswer, protocol.TypeCancel:
			go d.control(d.cfg.RunService.localCtx, m.Type, *m.Control)
		default:
			return fmt.Errorf("unexpected server message %s", m.Type)
		}
	}
}
func (d *endpointConnection) sessionDir(m session.Session) string {
	return filepath.Join(d.cfg.RunService.Sessions.Root, m.EndpointID, m.ConversationID, "sessions", m.ID)
}
func receiptName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".json"
}
func (d *endpointConnection) messageDir(conversation string) string {
	return filepath.Join(d.cfg.RunService.Sessions.Root, d.cfg.EndpointID, conversation, ".server", "messages")
}
func (d *endpointConnection) receiptPath(m session.Session, key string) string {
	return filepath.Join(d.messageDir(m.ConversationID), receiptName(key))
}
func (d *endpointConnection) saveReceipt(r messageReceipt) error {
	return fileutil.WriteJSON(filepath.Join(d.messageDir(r.Message.ConversationID), receiptName(r.Message.Key())), r)
}
func (d *endpointConnection) findReceipt(ctx context.Context, p protocol.ConversationMessage) (messageReceipt, bool, error) {
	var r messageReceipt
	raw, err := os.ReadFile(filepath.Join(d.messageDir(p.ConversationID), receiptName(p.Key())))
	if os.IsNotExist(err) {
		return d.readTombstone(p)
	}
	if err != nil {
		return r, false, err
	}
	err = json.Unmarshal(raw, &r)
	return r, err == nil, err
}
func (d *endpointConnection) enqueue(p protocol.ConversationMessage) error {
	if !p.Valid() || !fileutil.Segment(p.ConversationID) || !fileutil.Segment(p.AgentID) {
		return fileutil.ErrInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active[p.Key()] {
		current, _ := json.Marshal(p)
		for _, queue := range d.queues {
			for _, old := range queue {
				if old.Key() == p.Key() {
					original, _ := json.Marshal(old)
					if string(original) != string(current) {
						return fmt.Errorf("active message identity content conflict")
					}
				}
			}
		}
		return nil
	}
	key := p.ConversationID + "/" + p.AgentID + "/" + p.AgentVersionID
	d.active[p.Key()] = true
	d.queues[key] = append(d.queues[key], p)
	if len(d.queues[key]) == 1 {
		go func() {
			for {
				d.mu.Lock()
				queue := d.queues[key]
				if len(queue) == 0 {
					delete(d.queues, key)
					d.mu.Unlock()
					return
				}
				current := queue[0]
				d.mu.Unlock()
				d.receiveMessage(d.cfg.RunService.localCtx, current)
				d.mu.Lock()
				delete(d.active, current.Key())
				d.queues[key] = d.queues[key][1:]
				d.mu.Unlock()
			}
		}()
	}
	return nil
}
func (d *endpointConnection) receiveMessage(ctx context.Context, p protocol.ConversationMessage) {
	saved, found, err := d.findReceipt(ctx, p)
	var receiptMu sync.Mutex
	fail := func(state, reason string) {
		receiptMu.Lock()
		if saved.State == "accepted" {
			receiptMu.Unlock()
			return
		}
		saved.State = state
		saved.Error = reason
		persistErr := d.saveReceipt(saved)
		receiptMu.Unlock()
		if persistErr != nil {
			d.log.Printf("message receipt: %v", persistErr)
			return
		}
		status := protocol.AckError
		if state == "unknown" {
			status = protocol.AckUnknown
		}
		_ = d.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: p.Key(), Status: status, Error: reason})
	}

	if err != nil {
		_ = d.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: p.Key(), Status: protocol.AckUnknown, Error: err.Error()})
		return
	}
	if found {
		a, _ := json.Marshal(saved.Message)
		b, _ := json.Marshal(p)
		if string(a) != string(b) {
			_ = d.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: p.Key(), Status: protocol.AckError, Error: "message identity content conflict"})
			return
		}
		switch saved.State {
		case "accepted":
			if err := d.replayAccepted(ctx, saved); err != nil {
				_ = d.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: p.Key(), Status: protocol.AckUnknown, Error: err.Error()})
			}
			return
		case "submitting", "unknown":
			fail("unknown", "input may already be accepted; automatic replay stopped")
			return
		case "preparing":
			fail("unknown", "preparation is already owned by another operation")
			return
		}
	}
	saved = messageReceipt{Message: p, State: "preparing"}
	if err = d.ensureConversation(p.ConversationID); err != nil {
		_ = d.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: p.Key(), Status: protocol.AckError, Error: err.Error()})
		return
	}
	release, err := d.cfg.RunService.Sessions.HoldConversation(d.cfg.EndpointID, p.ConversationID)
	if err != nil {
		return
	}
	defer release()
	if err = d.saveReceipt(saved); err != nil {
		return
	}
	// Reserve capacity while configuration is fetched. startSession subsequently
	// acquires the execution slot; it independently enforces the global limit.
	existing := false
	d.cfg.RunService.localMu.Lock()
	for _, r := range d.cfg.RunService.localRuns {
		if r.session.EndpointID == d.cfg.EndpointID && r.session.ConversationID == p.ConversationID && r.session.AgentID == p.AgentID && r.session.AgentVersionID == p.AgentVersionID {
			existing = true
			break
		}
	}
	d.cfg.RunService.localMu.Unlock()
	reserved := false
	if !existing {
		select {
		case d.cfg.RunService.slots <- struct{}{}:
			reserved = true
		default:
			fail("rejected", ErrLocalBusy.Error())
			return
		}
	}
	request, err := d.resolveResources(ctx, p)
	if reserved {
		<-d.cfg.RunService.slots
	}
	if err != nil {
		fail("rejected", err.Error())
		return
	}
	request.Prompt = p.Text()
	request.agentID = p.AgentID
	request.agentVersionID = p.AgentVersionID
	request.environment = []string{"AGENTUSE_SERVER=" + d.cfg.Server, "AGENTUSE_TOKEN=" + d.cfg.Key, "AGENTUSE_CONFIG=" + d.cfg.ConfigPath, "AGENTUSE_WORK=" + p.WorkToken, "AGENTUSE_SESSION=" + p.ConversationID, "AGENTUSE_CONVERSATION=" + p.ConversationID, "AGENTUSE_AGENT=" + p.AgentID, "AGENTUSE_MESSAGE_ID=" + p.MessageID, "AGENTUSE_ENDPOINT=" + d.cfg.EndpointID, "AGENTUSE_RUN=", "AGENTUSE_ATTEMPT=", "AGENTUSE_PROMPT="}
	if exe, e := os.Executable(); e == nil {
		request.environment = append(request.environment, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	request.beforeSend = func(metadata session.Session) error {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		saved.SessionID = metadata.ID
		saved.State = "preparing"
		events := filepath.Join(d.sessionDir(metadata), "events.jsonl")
		if info, e := os.Stat(events); e == nil {
			saved.Offset = info.Size()
		} else if !os.IsNotExist(e) {
			return e
		}
		cursor := filepath.Join(d.sessionDir(metadata), ".server", "stream.json")
		if _, e := os.Stat(cursor); os.IsNotExist(e) {
			if e = fileutil.WriteJSON(cursor, streamReceipt{Offset: saved.Offset}); e != nil {
				return e
			}
		}
		if e := d.saveReceipt(saved); e != nil {
			return e
		}
		return d.syncAttachments(ctx, &protocol.ConversationMessage{ConversationID: p.ConversationID}, sessionWorkspace(d.cfg.RunService.Sessions.Root, d.cfg.EndpointID, p.ConversationID))
	}
	request.onSubmit = func() error {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		saved.State = "submitting"
		if err := d.saveTombstone(saved); err != nil {
			return err
		}
		return d.saveReceipt(saved)
	}
	request.accepted = func(a harness.Acceptance) error {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		saved.State = "accepted"
		saved.RunID = a.RunID
		saved.InputID = a.InputID
		if err := d.saveReceipt(saved); err != nil {
			return err
		}
		return d.saveTombstone(saved)
	}
	_, err = d.cfg.RunService.startSession(ctx, d.cfg.EndpointID, p.ConversationID, request, "")
	receiptMu.Lock()
	state := saved.State
	receiptMu.Unlock()
	if err != nil && state != "accepted" {
		if state == "submitting" {
			fail("unknown", err.Error())
		} else {
			fail("rejected", err.Error())
		}
	}
}
func (d *endpointConnection) ensureConversation(id string) error {
	root, err := fileutil.SafePath(d.cfg.RunService.Sessions.Root, d.cfg.EndpointID, id)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(root, "workspace"), 0700); err != nil {
		return err
	}
	marker := filepath.Join(root, ".agentuse-workspace.json")
	if _, err = os.Stat(marker); os.IsNotExist(err) {
		return fileutil.WriteJSON(marker, map[string]string{"endpoint_id": d.cfg.EndpointID, "conversation_id": id})
	}
	return err
}
func (d *endpointConnection) streamMetadata(ctx context.Context, id string) (session.Session, error) {
	conversations, err := d.cfg.RunService.Sessions.Conversations(ctx, d.cfg.EndpointID)
	if err != nil {
		return session.Session{}, err
	}
	for _, c := range conversations {
		m, e := d.cfg.RunService.Sessions.Session(ctx, d.cfg.EndpointID, c.ID, id)
		if e == nil {
			return m, nil
		}
		if !os.IsNotExist(e) {
			return m, e
		}
	}
	return session.Session{}, os.ErrNotExist
}
func (d *endpointConnection) ackEventID(id string) error {
	sessionID, offset, err := protocol.ParseEventID(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.replayPending[id] {
		delete(d.replayPending, id)
		d.mu.Unlock()
		return nil
	}
	expected := d.streamPending[sessionID]
	d.mu.Unlock()
	if expected == 0 || expected != offset {
		return fmt.Errorf("unexpected event acknowledgement %s", id)
	}
	return d.ackEvent(sessionID, offset)
}
func (d *endpointConnection) ackEvent(id string, offset int64) error {
	d.mu.Lock()
	expected := d.streamPending[id]
	d.mu.Unlock()
	if expected == 0 || expected != offset {
		return fmt.Errorf("unexpected event acknowledgement")
	}
	m, err := d.streamMetadata(context.Background(), id)
	if err != nil {
		return err
	}
	if err = fileutil.WriteJSON(filepath.Join(d.sessionDir(m), ".server", "stream.json"), streamReceipt{Offset: offset}); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.streamPending, id)
	d.mu.Unlock()
	return nil
}
func (d *endpointConnection) receipts(conversation string) ([]messageReceipt, error) {
	entries, err := os.ReadDir(d.messageDir(conversation))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []messageReceipt{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(d.messageDir(conversation), e.Name()))
		if e != nil {
			return nil, e
		}
		var r messageReceipt
		if e = json.Unmarshal(raw, &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (d *endpointConnection) replayAccepted(ctx context.Context, r messageReceipt) error {
	m, err := d.cfg.RunService.Sessions.Session(ctx, d.cfg.EndpointID, r.Message.ConversationID, r.SessionID)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(d.sessionDir(m), "events.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Seek(r.Offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	pos := r.Offset
	for {
		line, e := reader.ReadBytes('\n')
		if e != nil {
			return e
		}
		pos += int64(len(line))
		var event harness.Event
		if e = json.Unmarshal(line, &event); e != nil {
			return e
		}
		if event.Type == "accepted" && event.RunID == r.RunID && event.InputID == r.InputID {
			frame := protocol.EventFrame(protocol.ClientEvent{SessionID: m.ID, ConversationID: r.Message.ConversationID, AgentID: m.AgentID, AgentVersionID: m.AgentVersionID, Harness: m.Harness, Revision: m.HarnessRevision, MessageID: r.Message.MessageID, Seq: pos, Event: json.RawMessage(strings.TrimSpace(string(line)))})
			d.mu.Lock()
			if d.streamPending[m.ID] == pos {
				d.mu.Unlock()
				return nil
			}
			d.replayPending[frame.ID] = true
			d.mu.Unlock()
			return d.send(ctx, frame)
		}
	}
}
func (d *endpointConnection) flushEvents(ctx context.Context) error {
	conversations, err := d.cfg.RunService.Sessions.Conversations(ctx, d.cfg.EndpointID)
	if err != nil {
		return err
	}
	for _, c := range conversations {
		receipts, err := d.receipts(c.ID)
		if err != nil {
			return err
		}
		sessions, err := d.cfg.RunService.Sessions.Sessions(ctx, d.cfg.EndpointID, c.ID)
		if err != nil {
			return err
		}
		for _, m := range sessions {
			d.mu.Lock()
			pending := d.streamPending[m.ID] != 0
			d.mu.Unlock()
			if pending {
				continue
			}
			cursorPath := filepath.Join(d.sessionDir(m), ".server", "stream.json")
			var cursor streamReceipt
			raw, e := os.ReadFile(cursorPath)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &cursor); e != nil {
				return e
			}
			f, e := os.Open(filepath.Join(d.sessionDir(m), "events.jsonl"))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if _, e = f.Seek(cursor.Offset, io.SeekStart); e != nil {
				f.Close()
				return e
			}
			line, e := bufio.NewReader(f).ReadBytes('\n')
			f.Close()
			if e == io.EOF {
				continue
			}
			if e != nil {
				return e
			}
			var event harness.Event
			if e = json.Unmarshal(line, &event); e != nil {
				return e
			}
			envelope := protocol.ClientEvent{ConversationID: c.ID, AgentID: m.AgentID, AgentVersionID: m.AgentVersionID, SessionID: m.ID, Harness: m.Harness, Revision: m.HarnessRevision, Seq: cursor.Offset + int64(len(line)), Event: json.RawMessage(strings.TrimSuffix(string(line), "\n"))}
			if event.Type == "log" {
				// Diagnostics are best effort and do not pin a directory or need ACK.
				if cursor.Offset > 0 && event.RunID != "" {
					_ = d.send(ctx, protocol.EventFrame(envelope))
				}
				if e = fileutil.WriteJSON(cursorPath, streamReceipt{Offset: envelope.Seq}); e != nil {
					return e
				}
				continue
			}
			if event.Type == "accepted" {
				for _, r := range receipts {
					if r.SessionID == m.ID && r.RunID == event.RunID && r.InputID == event.InputID {
						envelope.MessageID = r.Message.MessageID
						break
					}
				}
				if envelope.MessageID == "" {
					id, e := d.lateAcceptance(ctx, c.ID, m.ID, event.RunID, event.InputID, envelope.Seq, receipts)
					if e != nil {
						return e
					}
					envelope.MessageID = id
					if id == "" {
						continue
					}
				}
			}
			d.mu.Lock()
			d.streamPending[m.ID] = envelope.Seq
			d.mu.Unlock()
			if e = d.send(ctx, protocol.EventFrame(envelope)); e != nil {
				return e
			}
		}
	}
	return nil
}
