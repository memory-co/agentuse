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
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/securews"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/service"
	deviceservice "github.com/memory-co/agentuse/server/internal/service/device"
)

type Hub struct {
	authBudget     float64
	authUpdated    time.Time
	service        *service.Service
	log            *log.Logger
	mu             sync.Mutex
	conns          map[string]*deviceConn
	clientUpgrades map[string]clientUpgrade
	authenticating chan struct{}
}
type deviceConn struct {
	d          agentuse.Device
	ws         *websocket.Conn
	channel    *securews.Channel
	peer       securews.Peer
	cancel     context.CancelFunc
	sendMu     sync.Mutex
	wake       chan struct{}
	capacity   protocol.Message
	canUpgrade bool
}

func New(s *service.Service, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	return &Hub{service: s, log: logger, authBudget: 128, authUpdated: time.Now(), conns: map[string]*deviceConn{}, clientUpgrades: map[string]clientUpgrade{}, authenticating: make(chan struct{}, 64)}
}
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(protocol.ProtocolHeader) != protocol.ExecutionProtocol {
		w.Header().Set(protocol.ProtocolHeader, protocol.ExecutionProtocol)
		http.Error(w, "client protocol upgrade required", http.StatusUpgradeRequired)
		return
	}
	if !h.allowAuthentication() {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "device authentication rate exceeded", http.StatusTooManyRequests)
		return
	}
	select {
	case h.authenticating <- struct{}{}:
	default:
		http.Error(w, "device authentication busy", http.StatusTooManyRequests)
		return
	}
	authSlot := true
	defer func() {
		if authSlot {
			<-h.authenticating
		}
	}()
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	channel, policy, err := securews.Accept(ctx, ws, h.service.Security.Identity, h.service.Security.WSEncryption)
	if err != nil {
		return
	}
	authCtx, authCancel := context.WithTimeout(ctx, 15*time.Second)
	var auth securews.Authentication
	channel.Limit = 64 << 10
	err = channel.Read(authCtx, &auth)
	authCancel()
	if err != nil {
		return
	}
	if auth.Type == "enroll" {
		d, cert, e := h.service.DeviceService.Enroll(ctx, policy, auth)
		if e != nil {
			_ = channel.Write(ctx, securews.AuthResult{Type: "rejected", Error: "device key is unknown, revoked or failed verification"})
			return
		}
		_ = channel.Write(ctx, securews.AuthResult{Type: "enrolled", DeviceID: d.ID, Name: d.Name, UserID: d.UserID, Certificate: cert})
		return
	}
	peer, err := securews.VerifyProof(h.service.Security.Identity.CA, policy, auth, h.service.Now())
	if err != nil {
		return
	}
	if policy.Encryption && (channel.TLSPeer == nil || channel.TLSPeer.SerialNumber.Cmp(peer.Certificate.SerialNumber) != 0) {
		return
	}
	d, err := h.service.DeviceService.AuthenticateCertificate(ctx, peer)
	if err != nil {
		_ = channel.Write(ctx, securews.AuthResult{Type: "rejected", Error: "device credential is revoked or expired"})
		return
	}
	if err = channel.Write(ctx, securews.AuthResult{Type: "authenticated", DeviceID: d.ID, Name: d.Name, UserID: d.UserID}); err != nil {
		return
	}
	deadlineCtx, deadlineCancel := context.WithDeadline(ctx, peer.NotAfter)
	defer deadlineCancel()
	ctx = deadlineCtx
	c := &deviceConn{d: d, ws: ws, channel: channel, peer: peer, cancel: cancel, wake: make(chan struct{}, 1)}
	go func() { <-ctx.Done(); ws.CloseNow() }()
	firstCtx, firstCancel := context.WithTimeout(ctx, 15*time.Second)
	var first protocol.Message
	err = channel.Read(firstCtx, &first)
	firstCancel()
	if err == nil && first.Type == protocol.TypeCredential && first.Operation == "renew" {
		raw, e := h.service.DeviceService.RenewCertificate(ctx, peer, first.CSR)
		response := protocol.Message{Type: protocol.TypeCredential, Operation: "renew", ID: first.ID, Certificate: raw, Status: protocol.AckOK}
		if e != nil {
			response.Status = protocol.AckError
			response.Error = "credential renewal failed"
		}
		_ = c.send(ctx, response)
		return
	}
	if err != nil || first.Type != protocol.TypeHeartbeat {
		return
	}
	c.d.ClientVersion = first.ClientVersion
	c.canUpgrade = first.ClientUpgrade
	channel.Limit = securews.MaxMessage
	<-h.authenticating
	authSlot = false
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
	if err = h.serve(ctx, c, first); err != nil {
		h.log.Printf("device %s disconnected: %v", d.ID, err)
	}
}
func (h *Hub) serve(ctx context.Context, c *deviceConn, first protocol.Message) error {
	reported := []deviceservice.ReportedHarness{}
	for _, a := range first.Harnesses {
		reported = append(reported, deviceservice.ReportedHarness{Name: a.Name, Version: a.Version, Revision: a.Revision, Capabilities: a.Capabilities})
	}
	caps := []string{protocol.ExecutionProtocol, "conversation-attachments-v1"}
	if c.canUpgrade {
		caps = append(caps, protocol.CapabilityClientUpgrade)
	}
	if _, err := h.service.DeviceService.DeviceConnected(ctx, c.d, reported, caps); err != nil {
		return err
	}
	h.clientReconnected(c)
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
			if err := c.channel.Read(ctx, &m); err != nil {
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
			if _, e := h.service.DeviceService.AuthenticateCertificate(ctx, c.peer); e != nil {
				return e
			}
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
				if ack.Status == protocol.AckOK {
					runID, credential, e := h.service.RunService.AccessForEvent(ctx, c.d.ID, *m.ClientEvent)
					if e != nil {
						return e
					}
					ack.RunID = runID
					ack.Access = credential
				}
				if err = c.send(ctx, ack); err != nil {
					return err
				}
			case protocol.TypeAccess:
				credential, e := h.service.RunService.RecoverAccess(ctx, c.d.ID, m.RunID)
				response := protocol.Message{Type: protocol.TypeAccess, ID: m.ID, RunID: m.RunID, Access: credential, Status: protocol.AckOK}
				if e != nil {
					response.Status = protocol.AckError
					response.Error = "execution access unavailable"
				}
				if err = c.send(ctx, response); err != nil {
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
			case protocol.TypeClientUpgrade:
				if m.Status == "" {
					return errors.New("missing client upgrade status")
				}
				h.reportClientUpgrade(c, m)
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
		if errors.Is(err, database.ErrNotFound) {
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
	return c.channel.Write(ctx, m)
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
	delete(h.clientUpgrades, id)
	h.mu.Unlock()
	if c != nil {
		c.cancel()
		c.ws.Close(websocket.StatusPolicyViolation, "device credential revoked")
	}
}

// A bounded global bucket limits unauthenticated cryptographic and registration
// work without retaining an unbounded map of attacker-controlled source IPs.
func (h *Hub) allowAuthentication() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	h.authBudget = min(128, h.authBudget+now.Sub(h.authUpdated).Seconds()*16)
	h.authUpdated = now
	if h.authBudget < 1 {
		return false
	}
	h.authBudget--
	return true
}
