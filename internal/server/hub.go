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

// hub 管所有在线 daemon 的 WebSocket 连接，负责派任务。
type hub struct {
	s     *Server
	mu    sync.Mutex
	conns map[string]*daemonConn // daemon id → 连接
}

type daemonConn struct {
	d      store.Daemon
	ws     *websocket.Conn
	cancel context.CancelFunc

	mu   sync.Mutex // 保护 idle,串行化领任务
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
			reported := make([]store.ReportedAgent, 0, len(m.Agents))
			for _, a := range m.Agents {
				reported = append(reported, store.ReportedAgent{Name: a.Name, Version: a.Version})
			}
			ignored, err := st.DaemonConnected(ctx, c.d, reported)
			if err != nil {
				return err
			}
			users, err := st.RequeueDaemonTasks(ctx, c.d.ID, m.Running)
			if err != nil {
				return err
			}
			if err := c.send(ctx, protocol.Message{Type: protocol.TypeWelcome, DaemonID: c.d.ID, Name: c.d.Name, Ignored: ignored}); err != nil {
				return err
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
			if m.TaskID != "" {
				if err := st.RenewLease(ctx, c.d.ID, m.TaskID, protocol.LeaseSeconds); err != nil {
					h.reportStale(ctx, c, m.TaskID, err)
				}
			}
		case protocol.TypeEvent:
			if err := st.AddEvent(ctx, c.d.ID, m.TaskID, m.Kind, m.Payload, protocol.LeaseSeconds); err != nil {
				h.reportStale(ctx, c, m.TaskID, err)
			}
		default:
			_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, Error: "unknown message type " + m.Type})
		}
	}
}

// reportStale 告诉 daemon 这个任务已经不归它了，它应该停掉。
func (h *hub) reportStale(ctx context.Context, c *daemonConn, taskID string, err error) {
	if !errors.Is(err, store.ErrStaleTask) {
		h.s.log.Printf("daemon %s task %s: %v", c.d.ID, taskID, err)
	}
	_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, TaskID: taskID, Error: err.Error()})
}

// dispatch 给空闲的 daemon 领一个任务推过去。
func (h *hub) dispatch(ctx context.Context, c *daemonConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.idle {
		return
	}
	t, err := h.s.store.ClaimTask(ctx, c.d, protocol.LeaseSeconds)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			h.s.log.Printf("claim for %s: %v", c.d.ID, err)
		}
		return
	}
	c.idle = false
	msg := protocol.Message{Type: protocol.TypeTask, Task: &protocol.Task{ID: t.ID, Agent: t.Agent, Input: t.Input}}
	if err := c.send(ctx, msg); err != nil {
		// 没送到:等租约过期自动收回。
		h.s.log.Printf("send task %s to %s: %v", t.ID, c.d.ID, err)
		return
	}
	h.s.log.Printf("任务 %s(%s)派给 daemon %s", t.ID, t.Agent, c.d.ID)
}

// kickUser 有新任务可派时，叫醒该用户所有空闲的 daemon。
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
