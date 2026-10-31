package hub

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/memory-co/agentuse/server/internal/service"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
)

// hub 管所有在线 daemon 的 WebSocket 连接，负责派活。
type Hub struct {
	service *service.Service
	log     *log.Logger
	mu      sync.Mutex
	conns   map[string]*daemonConn // daemon id → 连接
}

type daemonConn struct {
	d      store.Daemon
	ws     *websocket.Conn
	cancel context.CancelFunc

	mu   sync.Mutex // 保护 idle,串行化领活
	idle bool       // daemon 说过 ready、还没派活
}

func New(s *service.Service, logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	return &Hub{service: s, log: logger, conns: map[string]*daemonConn{}}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := h.service.DaemonByKey(r.Context(), key)
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

	h.log.Printf("daemon %s (%s) 已连接", d.ID, d.Name)
	err = h.serve(ctx, c)

	h.mu.Lock()
	if h.conns[d.ID] == c {
		delete(h.conns, d.ID)
		_ = h.service.DaemonDisconnected(context.Background(), d.ID)
	}
	h.mu.Unlock()
	ws.Close(websocket.StatusNormalClosure, "")
	h.log.Printf("daemon %s (%s) 已断开: %v", d.ID, d.Name, err)
}

func (h *Hub) serve(ctx context.Context, c *daemonConn) error {
	st := h.service
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
			ignored, err := st.DaemonConnected(ctx, c.d, reported, m.Capabilities)
			if err != nil {
				return err
			}
			live := map[string]string{}
			for _, ref := range m.ActiveRuns {
				live[ref.RunID] = ref.AttemptID
			}
			valid, err := st.ReconcileRuns(ctx, c.d.ID, live)
			if err != nil {
				return err
			}
			refs := []protocol.RunRef{}
			for _, run := range valid {
				refs = append(refs, protocol.RunRef{RunID: run.ID, AttemptID: run.AttemptID})
			}
			if err := c.send(ctx, protocol.Message{Type: protocol.TypeWelcome, DaemonID: c.d.ID, Name: c.d.Name, Ignored: ignored, ValidRuns: refs}); err != nil {
				return err
			}
			for _, run := range valid {
				ds, e := st.RunSteers(ctx, run.ID)
				if e != nil {
					return e
				}
				h.PushRunSteers(ds)
			}
			qs, e := st.PendingRunAnswers(ctx, c.d.ID)
			if e != nil {
				return e
			}
			for _, q := range qs {
				_ = c.send(ctx, protocol.Message{Type: protocol.TypeAnswer, Answer: &protocol.Answer{RunID: q.RunID, AttemptID: q.AttemptID, MessageID: q.MessageID, ToolUseID: q.ToolUseID, Answers: q.Answers}})
			}
		case protocol.TypeReady:
			c.mu.Lock()
			c.idle = true
			c.mu.Unlock()
			h.dispatch(ctx, c)
		case protocol.TypeHeartbeat:
			_ = st.TouchDaemon(ctx, c.d.ID)
			if m.RunID != "" {
				if err := st.RenewRun(ctx, c.d.ID, m.RunID, m.AttemptID, protocol.LeaseSeconds); err != nil {
					_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, RunID: m.RunID, AttemptID: m.AttemptID, MessageID: m.MessageID, Error: err.Error()})
				}
			}

		case protocol.TypeEvent:
			if m.RunID != "" {
				if err := st.ReportRunEvent(ctx, c.d.ID, m.RunID, m.AttemptID, m.EventSeq, m.Kind, m.Payload, protocol.LeaseSeconds); err != nil {
					_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, RunID: m.RunID, AttemptID: m.AttemptID, MessageID: m.MessageID, Error: err.Error()})
				}
				if ds, e := st.PendingRunSteers(ctx); e == nil {
					h.PushRunSteers(ds)
				}
				h.KickAll()
				continue
			}
			_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, Error: "run_id and attempt_id are required"})
		default:
			_ = c.send(ctx, protocol.Message{Type: protocol.TypeError, Error: "unknown message type " + m.Type})
		}
	}
}

func (h *Hub) dispatch(ctx context.Context, c *daemonConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.idle {
		return
	}
	clRun, runErr := h.service.ClaimRun(ctx, c.d, protocol.LeaseSeconds)
	if runErr == nil {
		c.idle = false
		run := clRun.Run
		msg := protocol.Message{Type: protocol.TypeRun, Run: &protocol.Dispatch{ID: run.MessageID, RunID: run.ID, AttemptID: run.AttemptID, SessionID: run.SessionID, AgentID: run.AgentKey, Harness: run.Harness, Input: clRun.Input, TaskMessageIDs: clRun.TaskMessageIDs, ContextSeq: run.ContextSeq, Instructions: clRun.Instructions, WorkToken: clRun.WorkToken}}
		if err := c.send(ctx, msg); err != nil {
			h.log.Printf("send run: %v", err)
		}
		return
	}
	if !errors.Is(runErr, store.ErrNotFound) {
		h.log.Printf("claim run: %v", runErr)
		return
	}
}

// KickUser 有新活可派时，叫醒该用户所有空闲的 daemon。
func (h *Hub) KickUser(userID string) {
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

// Disconnect 主动断开一台 daemon(删除 / 重置时)。
func (h *Hub) Disconnect(daemonID string) {
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

func (h *Hub) KickAll() {
	h.mu.Lock()
	cs := make([]*daemonConn, 0, len(h.conns))
	for _, c := range h.conns {
		cs = append(cs, c)
	}
	h.mu.Unlock()
	for _, c := range cs {
		go h.dispatch(context.Background(), c)
	}
}
func (h *Hub) PushRun(daemon string, m protocol.Message) {
	h.mu.Lock()
	c := h.conns[daemon]
	h.mu.Unlock()
	if c != nil {
		_ = c.send(context.Background(), m)
	}
}
func (h *Hub) PushRunSteers(ds []store.Delivery) {
	for _, d := range ds {
		if d.Kind != "steer" || d.Status != "pending" {
			continue
		}
		run, err := h.service.GetRun(context.Background(), store.User{Role: store.RoleAdmin}, d.SessionID, d.RunID)
		if err != nil || run.Status != "running" {
			continue
		}
		prompt, err := h.service.RunTaskPrompt(context.Background(), d.MessageID)
		if err != nil || prompt == "" {
			continue
		}
		h.PushRun(run.DaemonID, protocol.Message{Type: protocol.TypeSteer, Steer: &protocol.Steer{ID: d.ID, RunID: run.ID, AttemptID: run.AttemptID, MessageID: run.MessageID, Prompt: prompt}})
	}
}

func writeErr(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
