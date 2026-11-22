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
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/securews"
	"github.com/memory-co/agentuse/internal/version"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

var errEndpointCredential = errors.New("设备凭据无效，请重新注册")
var errEndpointProtocol = errors.New("client/server protocol upgrade required")

type endpointConnection struct {
	cfg           Config
	log           *log.Logger
	mu            sync.Mutex
	writeMu       sync.Mutex
	controlMu     sync.Mutex
	conn          *websocket.Conn
	channel       *securews.Channel
	tickets       map[string]*protocol.Access
	grants        map[string]*protocol.Access
	accessPending map[string]chan protocol.Message
	preparing     map[string]string
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
	if cfg.Server == "" || cfg.Credentials == nil || cfg.RunService == nil {
		return nil, errors.New("registered endpoint and run service required")
	}
	if logger == nil {
		logger = log.Default()
	}
	return &endpointConnection{cfg: cfg, log: logger, active: map[string]bool{}, queues: map[string][]protocol.ConversationMessage{}, streamPending: map[string]int64{}, replayPending: map[string]bool{}, tickets: map[string]*protocol.Access{}, grants: map[string]*protocol.Access{}, accessPending: map[string]chan protocol.Message{}, preparing: map[string]string{}}, nil
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
	c := d.channel
	d.mu.Unlock()
	if c == nil {
		return errors.New("endpoint offline")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.Write(ctx, m)
}
func (d *endpointConnection) heartbeat() protocol.Message {
	m := protocol.Message{Type: protocol.TypeHeartbeat, ID: "h:" + uuid.NewString(), FreeSessions: cap(d.cfg.RunService.slots) - len(d.cfg.RunService.slots), Harnesses: d.harnesses, ActiveStreams: []protocol.StreamRef{}}
	m.ClientVersion = version.Version
	m.ClientUpgrade = d.cfg.RunService.Upgrade != nil
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
	if _, e := securews.VerifyDevice(d.cfg.Credentials.CA, d.cfg.Credentials.Certificate, time.Now()); e != nil {
		return errEndpointCredential
	}
	channel, result, err := securews.Authenticate(ctx, d.cfg.Server, d.cfg.Credentials)
	if errors.Is(err, securews.ErrProtocol) {
		return errEndpointProtocol
	}
	if errors.Is(err, securews.ErrCredential) {
		return errEndpointCredential
	}
	if err != nil {
		return err
	}
	defer channel.Close()
	if d.cfg.DeviceID != "" && result.DeviceID != d.cfg.DeviceID {
		return errEndpointCredential
	}
	conn := channel.WS
	peer, e := securews.VerifyDevice(d.cfg.Credentials.CA, d.cfg.Credentials.Certificate, time.Now())
	if e != nil {
		return errEndpointCredential
	}
	if time.Until(peer.NotAfter) < 30*24*time.Hour {
		csr, e := d.cfg.Credentials.CSR()
		if e != nil {
			return e
		}
		renewCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		if e = channel.Write(renewCtx, protocol.Message{Type: protocol.TypeCredential, Operation: "renew", ID: "renew:" + uuid.NewString(), CSR: csr}); e != nil {
			return e
		}
		var response protocol.Message
		if e = channel.Read(renewCtx, &response); e != nil {
			return e
		}
		if response.Type != protocol.TypeCredential || response.Status != protocol.AckOK {
			return errEndpointCredential
		}
		renewed := *d.cfg.Credentials
		if _, e = renewed.SetCertificate(response.Certificate, time.Now()); e != nil {
			return e
		}
		if e = renewed.Save(d.cfg.CredentialsFile); e != nil {
			return e
		}
		d.cfg.Credentials = &renewed
		return errors.New("device certificate renewed; reconnecting")
	}
	d.mu.Lock()
	d.conn = conn
	d.channel = channel
	d.streamPending = map[string]int64{}
	d.replayPending = map[string]bool{}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		if d.conn == conn {
			d.conn = nil
			d.channel = nil
		}
		d.mu.Unlock()
	}()
	if err = d.send(ctx, d.heartbeat()); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var upgrades sync.WaitGroup
	defer func() { cancel(); <-done; upgrades.Wait() }()
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
				if time.Until(peer.NotAfter) < 30*24*time.Hour {
					conn.CloseNow()
					return
				}
				d.mu.Lock()
				for key, a := range d.tickets {
					if a.BindBefore > 0 && time.Now().Unix() >= a.BindBefore {
						delete(d.tickets, key)
					}
				}
				d.mu.Unlock()
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
		if err = channel.Read(ctx, &m); err != nil {
			return err
		}
		switch m.Type {
		case protocol.TypeMessage:
			if err = d.enqueue(*m.Delivery); err != nil {
				return err
			}
		case protocol.TypeAccess:
			d.mu.Lock()
			if m.Access != nil && m.Status == protocol.AckOK {
				d.grants[m.RunID] = m.Access
			}
			waiter := d.accessPending[m.ID]
			delete(d.accessPending, m.ID)
			d.mu.Unlock()
			if waiter != nil {
				waiter <- m
			}
		case protocol.TypeAck:
			if m.RunID != "" && m.Access != nil {
				d.mu.Lock()
				d.grants[m.RunID] = m.Access
				d.mu.Unlock()
			}
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
		case protocol.TypeClientUpgrade:
			if m.Status != "" {
				return fmt.Errorf("unexpected client upgrade status")
			}
			upgrades.Add(1)
			go func() {
				defer upgrades.Done()
				d.upgradeClient(runCtx, m)
			}()
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
	r.Message.Access = nil
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
	if p.Access != nil {
		d.tickets[p.Key()] = p.Access
	}
	p.Access = nil
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
	s := d.cfg.RunService
	if !s.upgradeGate.TryRLock() {
		// The Server will resend this unacknowledged message after reconnect.
		return
	}
	defer s.upgradeGate.RUnlock()
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
	request.environment = []string{"AGENTUSE_SERVER=" + d.cfg.Server, "AGENTUSE_TOKEN=", "AGENTUSE_CONFIG=" + d.cfg.ConfigPath, "AGENTUSE_CONVERSATION=" + p.ConversationID, "AGENTUSE_AGENT=" + p.AgentID, "AGENTUSE_MESSAGE_ID=" + p.MessageID, "AGENTUSE_ENDPOINT=" + d.cfg.EndpointID, "AGENTUSE_RUN=", "AGENTUSE_ATTEMPT=", "AGENTUSE_PROMPT="}
	if exe, e := os.Executable(); e == nil {
		request.environment = append(request.environment, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	request.beforeSend = func(metadata session.Session) error {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		saved.SessionID = metadata.ID
		d.mu.Lock()
		d.preparing[metadata.ID] = p.Key()
		d.mu.Unlock()
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
		return nil
	}
	request.preparePrompt = func(metadata session.Session) (string, error) {
		client := agentuse.New(d.cfg.Server, d.ticket(p.Key()))
		client.HTTPClient = &http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		view, err := client.GetMessageContext(ctx, p.ConversationID, p.MessageID, p.AgentID, metadata.ID)
		if err != nil {
			return "", err
		}
		prepared := p
		prepared.Context += completeMessagesText(view.CompleteMessages)
		return prepared.Text(), nil
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
		d.mu.Lock()
		delete(d.preparing, saved.SessionID)
		d.mu.Unlock()
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
			frame := protocol.EventFrame(protocol.ClientEvent{AccessToken: d.ticket(r.Message.Key()), SessionID: m.ID, ConversationID: r.Message.ConversationID, AgentID: m.AgentID, AgentVersionID: m.AgentVersionID, Harness: m.Harness, Revision: m.HarnessRevision, MessageID: r.Message.MessageID, Seq: pos, Event: json.RawMessage(strings.TrimSpace(string(line)))})
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
			if event.Type == "result" {
				d.mu.Lock()
				delete(d.grants, event.RunID)
				for _, receipt := range receipts {
					if receipt.RunID == event.RunID {
						delete(d.tickets, receipt.Message.Key())
					}
				}
				d.mu.Unlock()
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
						envelope.AccessToken = d.ticket(r.Message.Key())
						break
					}
				}
				if envelope.MessageID == "" {
					id, e := d.lateAcceptance(ctx, c.ID, m.ID, event.RunID, event.InputID, envelope.Seq, receipts)
					if e != nil {
						return e
					}
					envelope.MessageID = id
					envelope.AccessToken = d.ticket(protocol.ConversationMessage{ConversationID: c.ID, MessageID: id, AgentID: m.AgentID}.Key())
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
