package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/store"
)

// hub 管所有在线 daemon 的 WebSocket 连接，负责派活。
type hub struct {
	s     *Server
	mu    sync.Mutex
	conns map[string]*daemonConn // daemon id → 连接
}

type daemonConn struct {
	d      store.Daemon
	ws     *websocket.Conn
	cancel context.CancelFunc

	mu   sync.Mutex // 保护 idle,串行化领活
	idle bool       // daemon 说过 ready、还没派活
}

func newHub(s *Server) *hub { return &hub{s: s, conns: map[string]*daemonConn{}} }

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := h.s.store.DaemonByKey(r.Context(), key)
	if key == "" || err != nil {
		writeErr(w, http.StatusUnauthorized, "daemon 密钥无效，请重新运行安装命令")
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(8 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	c := &daemonConn{d: d, ws: ws, cancel: cancel}
	defer cancel()

	h.mu.Lock()
	if old := h.conns[d.ID]; old != nil {
		old.cancel()
		old.ws.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
	h.conns[d.ID] = c
	h.mu.Unlock()

	h.s.log.Printf("daemon %s (%s) 已连接", d.ID, d.Name)
	err = h.serve(ctx, c)

	h.mu.Lock()
	if h.conns[d.ID] == c {
		delete(h.conns, d.ID)
		_ = h.s.store.DaemonDisconnected(context.Background(), d.ID)
	}
	h.mu.Unlock()
	ws.Close(websocket.StatusNormalClosure, "")
	h.s.log.Printf("daemon %s (%s) 已断开: %v", d.ID, d.Name, err)
}

func (h *hub) serve(ctx context.Context, c *daemonConn) error {
	st := h.s.store
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, c.ws, &m); err != nil {
			return err
		}
		switch m.Type {
		case protocol.TypeHello:
			reported := make([]store.ReportedHarness, 0, len(m.Harnesses))
			for _, a := range m.Harnesses {
				reported = append(reported, store.ReportedHarness{Name: a.Name, Version: a.Version})
			}
			ignored, err := st.DaemonConnected(ctx, c.d, reported)
			if err != nil {
				return err
			}
			users, err := st.RequeueDaemonSessions(ctx, c.d.ID, m.Running)
			if err != nil {
				return err
			}
			if err := c.send(ctx, protocol.Message{Type: protocol.TypeWelcome, DaemonID: c.d.ID, Name: c.d.Name, Ignored: ignored}); err != nil {
				return err
			}
			// 断线期间答了的提问，再推一遍
			answers, err := st.PendingAnswers(ctx, c.d.ID)
			if err != nil {
				return err
			}
			for _, q := range answers {
				_ = c.send(ctx, answerMessage(q))
			}
			for _, u := range users {
				h.kickUser(u)
			}
		case protocol.TypeReady:
			c.mu.Lock()
			c.idle = true
			c.mu.Unlock()
			h.dispatch(ctx, c)
		case protocol.TypeHeartbeat:
			_ = st.TouchDaemon(ctx, c.d.ID)
			if m.MessageID != 0 {
				if err := st.RenewLease(ctx, c.d.ID, m.MessageID, protocol.LeaseSeconds); err != nil {
					h.reportStale(ctx, c, m.MessageID, err)
				}
			}
		case protocol.TypeEvent:
			steers, err := st.ReportEvent(ctx, c.d.ID, m.MessageID, m.Kind, m.Payload, protocol.LeaseSeconds)
			if err != nil {
				h.reportStale(ctx, c, m.MessageID, err)
			}
			for _, s := range steers {
				_ = c.send(ctx, steerMessage(s))
			}
		default:
			_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, Error: "unknown message type " + m.Type})
		}
	}
}

// reportStale 告诉 daemon 这条消息已经不归它了，它应该停掉。
func (h *hub) reportStale(ctx context.Context, c *daemonConn, messageID int64, err error) {
	if !errors.Is(err, store.ErrStaleMessage) {
		h.s.log.Printf("daemon %s message %d: %v", c.d.ID, messageID, err)
	}
	_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, MessageID: messageID, Error: err.Error()})
}

// dispatch 给空闲的 daemon 领一个会话，把最后一条用户消息推过去。
func (h *hub) dispatch(ctx context.Context, c *daemonConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.idle {
		return
	}
	cl, err := h.s.store.ClaimSession(ctx, c.d, protocol.LeaseSeconds)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			h.s.log.Printf("claim for %s: %v", c.d.ID, err)
		}
		return
	}
	c.idle = false
	ss := cl.Session
	msg := protocol.Message{Type: protocol.TypeMessage, Message: &protocol.Dispatch{
		ID: cl.Message.ID, SessionID: ss.ID, Harness: ss.Harness, Resume: ss.HarnessSessionID, Input: cl.Message.Content,
	}}
	if err := c.send(ctx, msg); err != nil {
		// 没送到:等租约过期自动收回。
		h.s.log.Printf("send session %s to %s: %v", ss.ID, c.d.ID, err)
		return
	}
	h.s.log.Printf("会话 %s(%s)的消息 %d 派给 daemon %s", ss.ID, ss.Harness, cl.Message.ID, c.d.ID)
	// 派出前就有的插话，跟着推过去
	steers, err := h.s.store.PendingSteers(ctx, cl.Message.ID)
	if err != nil {
		h.s.log.Printf("steers of message %d: %v", cl.Message.ID, err)
	}
	for _, st := range steers {
		_ = c.send(ctx, steerMessage(st))
	}
}

func steerMessage(st store.Steer) protocol.Message {
	return protocol.Message{Type: protocol.TypeSteer, Steer: &protocol.Steer{ID: st.ID, MessageID: st.MessageID, Prompt: st.Prompt}}
}

// pushSteer 把插话推给正在跑这个会话的 daemon;它不在线就算了，这一轮结束时插话会转成下一条消息。
func (h *hub) pushSteer(daemonID string, st store.Steer) {
	h.mu.Lock()
	c := h.conns[daemonID]
	h.mu.Unlock()
	if c != nil {
		_ = c.send(context.Background(), steerMessage(st))
	}
}

func answerMessage(q store.Question) protocol.Message {
	return protocol.Message{Type: protocol.TypeAnswer, Answer: &protocol.Answer{MessageID: q.MessageID, ToolUseID: q.ToolUseID, Answers: q.Answers}}
}

// pushAnswer 把答案推给正在等的 daemon;它不在线就等它重连时再推。
func (h *hub) pushAnswer(daemonID string, q store.Question) {
	h.mu.Lock()
	c := h.conns[daemonID]
	h.mu.Unlock()
	if c != nil {
		_ = c.send(context.Background(), answerMessage(q))
	}
}

// kickUser 有新活可派时，叫醒该用户所有空闲的 daemon。
func (h *hub) kickUser(userID string) {
	h.mu.Lock()
	var cs []*daemonConn
	for _, c := range h.conns {
		if c.d.UserID == userID {
			cs = append(cs, c)
		}
	}
	h.mu.Unlock()
	for _, c := range cs {
		go h.dispatch(context.Background(), c)
	}
}

// disconnect 主动断开一台 daemon(删除 / 重置时)。
func (h *hub) disconnect(daemonID string) {
	h.mu.Lock()
	c := h.conns[daemonID]
	h.mu.Unlock()
	if c != nil {
		c.cancel()
		c.ws.Close(websocket.StatusPolicyViolation, "daemon removed or reset")
	}
}

func (c *daemonConn) send(ctx context.Context, m protocol.Message) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, b)
}
