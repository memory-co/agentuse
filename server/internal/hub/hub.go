// Package hub transports saved conversation messages and public execution events.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

type Hub struct {
	service *service.Service
	log     *log.Logger
	mu      sync.Mutex
	conns   map[string]*deviceConn
}
type deviceConn struct {
	d        service.Device
	ws       *websocket.Conn
	cancel   context.CancelFunc
	sendMu   sync.Mutex
	wake     chan struct{}
	capacity protocol.Message
}

func New(s *service.Service, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	return &Hub{service: s, log: logger, conns: map[string]*deviceConn{}}
}
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := h.service.DeviceService.DeviceByKey(r.Context(), key)
	if err != nil || key == "" {
		http.Error(w, "invalid device credential", 401)
		return
	}
	if r.Header.Get(protocol.ProtocolHeader) != protocol.ExecutionProtocol {
		w.Header().Set(protocol.ProtocolHeader, protocol.ExecutionProtocol)
		http.Error(w, "client protocol upgrade required", http.StatusUpgradeRequired)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(8 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &deviceConn{d: d, ws: ws, cancel: cancel, wake: make(chan struct{}, 1)}
	h.mu.Lock()
	old := h.conns[d.ID]
	h.conns[d.ID] = c
	h.mu.Unlock()
	if old != nil {
		old.cancel()
		old.ws.Close(websocket.StatusPolicyViolation, "connection replaced")
	}
	defer func() {
		h.mu.Lock()
		if h.conns[d.ID] == c {
			delete(h.conns, d.ID)
			_ = h.service.DeviceService.DeviceDisconnected(context.Background(), d.ID)
		}
		h.mu.Unlock()
		ws.Close(websocket.StatusNormalClosure, "")
	}()
	if err = h.serve(ctx, c); err != nil {
		h.log.Printf("device %s disconnected: %v", d.ID, err)
	}
}
func (h *Hub) serve(ctx context.Context, c *deviceConn) error {
	var first protocol.Message
	if err := wsjson.Read(ctx, c.ws, &first); err != nil {
		return err
	}
	if first.Type != protocol.TypeHeartbeat {
		return errors.New("heartbeat required")
	}
	reported := []service.ReportedHarness{}
	for _, a := range first.Harnesses {
		reported = append(reported, service.ReportedHarness{Name: a.Name, Version: a.Version, Revision: a.Revision, Capabilities: a.Capabilities})
	}
	if _, err := h.service.DeviceService.DeviceConnected(ctx, c.d, reported, []string{protocol.ExecutionProtocol, "conversation-attachments-v1"}); err != nil {
		return err
	}
	c.capacity = first
	if err := c.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: first.ID, Status: protocol.AckOK}); err != nil {
		return err
	}
	pending, err := h.service.RunService.PendingMessages(ctx, c.d.ID)
	if err != nil {
		return err
	}
	for _, m := range pending {
		c.reserve(m)
		if err = c.send(ctx, protocol.DeliveryFrame(m)); err != nil {
			return err
		}
	}
	controls, err := h.service.RunService.PendingControls(ctx, c.d.ID)
	if err != nil {
		return err
	}
	for _, m := range controls {
		if err = c.send(ctx, m); err != nil {
			return err
		}
	}
	incoming := make(chan protocol.Message, 1)
	readError := make(chan error, 1)
	go func() {
		for {
			var m protocol.Message
			if err := wsjson.Read(ctx, c.ws, &m); err != nil {
				select {
				case readError <- err:
				case <-ctx.Done():
				}
				return
			}
			select {
			case incoming <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	if err = h.dispatch(ctx, c); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readError:
			return err
		case <-c.wake:
		case <-tick.C:
		case m := <-incoming:
			switch m.Type {
			case protocol.TypeHeartbeat:
				// Executor availability is frozen for this connection; capacity is a snapshot.
				if !sameHarnesses(first.Harnesses, m.Harnesses) {
					return errors.New("harness snapshot changed; reconnect required")
				}
				c.capacity = m
				// Outstanding new bindings may not yet be visible in the received snapshot.
				outstanding, e := h.service.RunService.PendingMessages(ctx, c.d.ID)
				if e != nil {
					return e
				}
				for _, p := range outstanding {
					c.reserve(p)
				}
				if err = h.service.DeviceService.TouchDevice(ctx, c.d.ID); err != nil {
					return err
				}
				if err = c.send(ctx, protocol.Message{Type: protocol.TypeAck, ID: m.ID, Status: protocol.AckOK}); err != nil {
					return err
				}
			case protocol.TypeEvent:
				if m.ClientEvent == nil {
					return errors.New("missing event")
				}
				err = h.service.ConversationService.ReportClientEvent(ctx, c.d.ID, *m.ClientEvent)
				var event struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(m.ClientEvent.Event, &event)
				if err == nil && event.Type == "log" {
					continue
				}
				ack := protocol.Message{Type: protocol.TypeAck, ID: m.ID, Status: protocol.AckOK}
				if err != nil {
					ack.Status = protocol.AckError
					ack.Error = err.Error()
				}
				if err = c.send(ctx, ack); err != nil {
					return err
				}
			case protocol.TypeAck:
				if strings.HasPrefix(m.ID, "m:") {
					err = h.service.RunService.RejectMessage(ctx, c.d.ID, m.ID, m.Status, m.Error)
				} else {
					err = h.service.RunService.ConfirmControl(ctx, c.d.ID, m.ID, m.Status, m.Error)
				}
				if err != nil {
					return err
				}
			default:
				return errors.New("unexpected client message")
			}
		}
		if err = h.dispatch(ctx, c); err != nil {
			return err
		}
	}
}
func sameHarnesses(a, b []protocol.Harness) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}
func (c *deviceConn) reserve(m protocol.ConversationMessage) {
	for _, a := range c.capacity.ActiveStreams {
		if a.ConversationID == m.ConversationID && a.AgentID == m.AgentID && a.AgentVersionID == m.AgentVersionID {
			return
		}
	}
	c.capacity.FreeSessions = max(0, c.capacity.FreeSessions-1)
	c.capacity.ActiveStreams = append(c.capacity.ActiveStreams, protocol.StreamRef{ConversationID: m.ConversationID, AgentID: m.AgentID, AgentVersionID: m.AgentVersionID})
}
func (h *Hub) dispatch(ctx context.Context, c *deviceConn) error {
	for {
		m, err := h.service.RunService.DispatchMessage(ctx, c.d, c.capacity)
		if errors.Is(err, service.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		c.reserve(m)
		if err = c.send(ctx, protocol.DeliveryFrame(m)); err != nil {
			return err
		}
	}
}
func (c *deviceConn) send(ctx context.Context, m protocol.Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c.ws, m)
}
func (h *Hub) PushRun(id string, m protocol.Message) {
	h.mu.Lock()
	c := h.conns[id]
	h.mu.Unlock()
	if c != nil {
		go func() {
			if err := c.send(context.Background(), m); err != nil {
				h.log.Printf("device delivery: %v", err)
			}
		}()
	}
}
func (h *Hub) KickAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}
func (h *Hub) KickUser(string) { h.KickAll() }
func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	c := h.conns[id]
	h.mu.Unlock()
	if c != nil {
		c.cancel()
		c.ws.Close(websocket.StatusPolicyViolation, "device credential revoked")
	}
}
