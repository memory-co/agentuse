// Package hub transports server message deliveries and unchanged client events.
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
	d      service.Daemon
	ws     *websocket.Conn
	cancel context.CancelFunc
	sendMu sync.Mutex
}

func New(s *service.Service, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	return &Hub{service: s, log: logger, conns: map[string]*deviceConn{}}
}
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := h.service.DaemonByKey(r.Context(), key)
	if err != nil || key == "" {
		http.Error(w, "invalid device credential", 401)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(8 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &deviceConn{d: d, ws: ws, cancel: cancel}
	h.mu.Lock()
	if old := h.conns[d.ID]; old != nil {
		old.cancel()
		old.ws.Close(websocket.StatusPolicyViolation, "connection replaced")
	}
	h.conns[d.ID] = c
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.conns[d.ID] == c {
			delete(h.conns, d.ID)
			_ = h.service.DaemonDisconnected(context.Background(), d.ID)
		}
		h.mu.Unlock()
		ws.Close(websocket.StatusNormalClosure, "")
	}()
	if err = h.serve(ctx, c); err != nil {
		h.log.Printf("device %s disconnected: %v", d.ID, err)
	}
}
func (h *Hub) serve(ctx context.Context, c *deviceConn) error {
	var hello protocol.Message
	if err := wsjson.Read(ctx, c.ws, &hello); err != nil {
		return err
	}
	if hello.Type != protocol.TypeHello || hello.Protocol != protocol.ExecutionProtocol {
		return errors.New("client execution protocol upgrade required")
	}
	reported := []service.ReportedHarness{}
	for _, a := range hello.Harnesses {
		reported = append(reported, service.ReportedHarness{Name: a.Name, Version: a.Version, Revision: a.Revision})
	}
	if _, err := h.service.DaemonConnected(ctx, c.d, reported, hello.Capabilities); err != nil {
		return err
	}
	if err := c.send(ctx, protocol.Message{Type: protocol.TypeWelcome, Protocol: protocol.ExecutionProtocol, DaemonID: c.d.ID, Name: c.d.Name}); err != nil {
		return err
	}
	prompts, err := h.service.PendingPrompts(ctx, c.d.ID)
	if err != nil {
		return err
	}
	for _, p := range prompts {
		if err = c.send(ctx, protocol.Message{Type: protocol.TypePrompt, Prompt: &p}); err != nil {
			return err
		}
	}
	controls, err := h.service.PendingControls(ctx, c.d.ID)
	if err != nil {
		return err
	}
	for _, m := range controls {
		if err = c.send(ctx, m); err != nil {
			return err
		}
	}
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, c.ws, &m); err != nil {
			return err
		}
		switch m.Type {
		case protocol.TypeClaim:
			prompt, err := h.service.ClaimPrompt(ctx, c.d, m)
			if errors.Is(err, service.ErrNotFound) {
				err = c.send(ctx, protocol.Message{Type: protocol.TypeNoTask, ClaimID: m.ClaimID})
			} else if err == nil {
				err = c.send(ctx, protocol.Message{Type: protocol.TypePrompt, ClaimID: m.ClaimID, Prompt: &prompt})
			}
			if err != nil {
				return err
			}
		case protocol.TypeHeartbeat:
			if err = h.service.TouchDaemon(ctx, c.d.ID); err != nil {
				return err
			}
		case protocol.TypeEvent:
			if m.ClientEvent == nil {
				return errors.New("missing client event")
			}
			err = h.service.ReportClientEvent(ctx, c.d.ID, *m.ClientEvent)
			if err != nil {
				if e := c.send(ctx, protocol.Message{Type: protocol.TypeError, SessionID: m.ClientEvent.SessionID, AckSeq: m.ClientEvent.Seq, Error: err.Error()}); e != nil {
					return e
				}
				continue
			}
			if err = c.send(ctx, protocol.Message{Type: protocol.TypeEventAck, SessionID: m.ClientEvent.SessionID, AckSeq: m.ClientEvent.Seq}); err != nil {
				return err
			}
		case protocol.TypePromptError:
			if err = h.service.RejectPrompt(ctx, c.d.ID, m.PromptID, m.Error); err != nil {
				return err
			}
			if err = c.send(ctx, protocol.Message{Type: protocol.TypePromptErrorAck, PromptID: m.PromptID, Prompt: m.Prompt}); err != nil {
				return err
			}
		case protocol.TypeControlAck:
			if m.Control == nil {
				return errors.New("missing control receipt")
			}
			if err = h.service.ConfirmControl(ctx, c.d.ID, m.Control.ID, m.Error); err != nil {
				return err
			}
		default:
			return errors.New("unknown execution message")
		}
	}
}
func (c *deviceConn) send(ctx context.Context, m protocol.Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, data)
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
	ids := []string{}
	for id := range h.conns {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.PushRun(id, protocol.Message{Type: protocol.TypeReady})
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
