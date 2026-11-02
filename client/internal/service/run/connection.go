package run

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/version"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type endpointConnection struct {
	ledger  store.Writer
	cfg     Config
	log     *log.Logger
	workdir string // configured conversation parent root

	mu        sync.Mutex
	claimMode bool
	claimID   string
	claimAt   time.Time
	harnesses map[string]harness.Adapter
	running   map[string]*runningMsg       // active executions keyed by run/attempt
	conn      func(protocol.Message) error // 当前连接的发送函数，断线时为 nil
	outbox    []protocol.Message           // 断线期间跑完的结果，重连后补发
}

var errOffline = errors.New("未连接")

// send 发到当前连接。断线时进度直接丢掉，最终结果留到重连后补发。
func (d *endpointConnection) send(m protocol.Message) error {
	durable := m.Type == protocol.TypeEvent && (m.RunID != "" || m.Kind == "done" || m.Kind == "failed")
	d.mu.Lock()
	conn := d.conn
	if conn == nil && durable {
		d.outbox = append(d.outbox, m)
	}
	d.mu.Unlock()
	if conn == nil {
		return errOffline
	}
	err := conn(m)
	if err != nil && durable {
		d.mu.Lock()
		d.outbox = append(d.outbox, m)
		d.mu.Unlock()
	}
	return err
}

func newConnection(cfg Config, logger *log.Logger, writers ...store.Writer) (*endpointConnection, error) {
	if cfg.Server == "" || cfg.Key == "" {
		return nil, errors.New("daemon 还没注册：请先运行页面上的安装命令(或 agentuse register)")
	}
	if logger == nil {
		logger = log.Default()
	}
	if cfg.Harness == nil {
		cfg.Harness = &harness.Service{Root: harness.HarnessRoot(cfg.HarnessConfig())}
	}
	if err := cfg.Harness.Freeze(cfg.Harnesses); err != nil {
		return nil, err
	}

	if cfg.Slots == nil {
		cfg.Slots = make(chan struct{}, 2)
	}
	home, _ := os.UserHomeDir()
	var ledger store.Writer
	if len(writers) > 0 {
		ledger = writers[0]
	}
	root := cfg.Settings.Session.WorkRoot
	if root == "" {
		root = filepath.Join(home, ".agentuse", "tmp")
	}
	if cfg.EndpointID == "" {
		_, cfg.EndpointID, _ = store.NormalizeEndpoint(cfg.Server)
	}
	d := &endpointConnection{ledger: ledger, cfg: cfg, log: logger, workdir: root, running: map[string]*runningMsg{}}
	if cfg.LocalStore != nil {
		for _, b := range cfg.LocalStore.Pending(cfg.EndpointID) {
			var m protocol.Message
			if json.Unmarshal(b, &m) == nil {
				d.outbox = append(d.outbox, m)
			}
		}
	}
	return d, nil
}

// Run 连上服务干活，断线自动退避重连，直到 ctx 结束。
func (d *endpointConnection) Run(ctx context.Context) error {
	defer func() {
		d.mu.Lock()
		active := make([]*runningMsg, 0, len(d.running))
		for _, r := range d.running {
			active = append(active, r)
			r.cancel()
		}
		d.mu.Unlock()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for _, r := range active {
			select {
			case <-r.done:
			case <-deadline.C:
				return
			}
		}
	}()
	backoff := time.Second
	for {
		start := time.Now()
		err := d.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errUnauthorized) {
			return err
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		d.log.Printf("连接断开: %v;%s 后重连", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

var errUnauthorized = endpoint.ErrUnauthorized

func wsURL(server string) string {
	switch {
	case strings.HasPrefix(server, "https://"):
		return "wss://" + strings.TrimPrefix(server, "https://") + protocol.WSPath
	case strings.HasPrefix(server, "http://"):
		return "ws://" + strings.TrimPrefix(server, "http://") + protocol.WSPath
	}
	return server + protocol.WSPath
}

// session 是一次连接的生命周期。
func (d *endpointConnection) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, resp, err := websocket.Dial(dialCtx, wsURL(d.cfg.Server), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + d.cfg.Key}},
	})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errUnauthorized
		}
		return err
	}
	ws.SetReadLimit(8 << 20)
	defer ws.CloseNow()
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	var wmu sync.Mutex
	send := func(m protocol.Message) error {
		wmu.Lock()
		defer wmu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return wsjson.Write(wctx, ws, m)
	}

	d.mu.Lock()
	d.harnesses = d.cfg.Harness.Available()
	hello := protocol.Message{Type: protocol.TypeHello, Version: version.Version, Capabilities: []string{attachment.Capability, "session-runs-v1", "codex-steer-v1"}}
	for _, a := range d.harnesses {
		hello.Harnesses = append(hello.Harnesses, protocol.Harness{Name: a.Name, Version: a.Version})
	}
	sort.Slice(hello.Harnesses, func(i, j int) bool { return hello.Harnesses[i].Name < hello.Harnesses[j].Name })
	for _, r := range d.running {
		hello.ActiveRuns = append(hello.ActiveRuns, protocol.RunRef{RunID: r.dispatch.RunID, AttemptID: r.dispatch.AttemptID})
	}
	for _, m := range d.outbox {
		hello.ActiveRuns = append(hello.ActiveRuns, protocol.RunRef{RunID: m.RunID, AttemptID: m.AttemptID})
	}
	d.mu.Unlock()
	if err := send(hello); err != nil {
		return err
	}
	defer func() {
		d.mu.Lock()
		d.conn = nil
		if d.claimID != "" {
			d.claimID = ""
			<-d.cfg.Slots
		}
		d.mu.Unlock()
	}()

	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				d.mu.Lock()
				if !d.claimMode || d.conn == nil {
					d.mu.Unlock()
					continue
				}
				if d.claimID != "" {
					expired := time.Since(d.claimAt) > 10*time.Second
					d.mu.Unlock()
					if expired {
						ws.CloseNow()
						return
					}
					continue
				}
				select {
				case d.cfg.Slots <- struct{}{}:
				default:
					d.mu.Unlock()
					continue
				}
				id := uuid.NewString()
				d.claimID = id
				d.claimAt = time.Now()
				d.mu.Unlock()
				if send(protocol.Message{Type: protocol.TypeClaim, ClaimID: id}) != nil {
					ws.CloseNow()
					return
				}
			}
		}
	}()
	// 心跳:有活时续租，没活时保活。
	go func() {
		t := time.NewTicker(protocol.HeartbeatSeconds * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.mu.Lock()
				beats := []protocol.Message{}
				for _, r := range d.running {
					beats = append(beats, protocol.Message{Type: protocol.TypeHeartbeat, MessageID: r.dispatch.ID, RunID: r.dispatch.RunID, AttemptID: r.dispatch.AttemptID})
				}
				d.mu.Unlock()
				if len(beats) == 0 {
					beats = append(beats, protocol.Message{Type: protocol.TypeHeartbeat})
				}
				for _, beat := range beats {
					_ = send(beat)
				}
			}
		}
	}()

	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, ws, &m); err != nil {
			return err
		}
		switch m.Type {
		case protocol.TypeWelcome:
			names := make([]string, 0, len(hello.Harnesses))
			for _, a := range hello.Harnesses {
				names = append(names, a.Name)
			}
			d.log.Printf("已连上 %s,daemon %s(%s),本机 harness: %v", d.cfg.Server, m.DaemonID, m.Name, names)
			if len(m.Ignored) > 0 {
				d.log.Printf("这些 harness 服务上没登记，已忽略: %v", m.Ignored)
			}
			d.mu.Lock()
			d.conn = send
			d.claimMode = false
			for _, cap := range m.Capabilities {
				if cap == "bounded-claim-v1" {
					d.claimMode = true
				}
			}
			valid := map[string]string{}
			for _, ref := range m.ValidRuns {
				valid[ref.RunID] = ref.AttemptID
			}
			for _, r := range d.running {
				if r.dispatch.RunID != "" && valid[r.dispatch.RunID] != r.dispatch.AttemptID {
					r.cancel()
				}
			}
			outbox := d.outbox
			d.outbox = nil
			busy := len(d.running) > 0
			d.mu.Unlock()
			for i, m := range outbox {
				if err := send(m); err != nil {
					d.mu.Lock()
					d.outbox = append(outbox[i:], d.outbox...)
					d.mu.Unlock()
					return err
				}
			}
			if !busy && !d.isPaused() {
				if err := send(protocol.Message{Type: protocol.TypeReady}); err != nil {
					return err
				}
			}
		case protocol.TypeNoTask:
			d.mu.Lock()
			if d.claimID != "" && d.claimID == m.ClaimID {
				d.claimID = ""
				<-d.cfg.Slots
			}
			d.mu.Unlock()
		case protocol.TypeRun:
			if m.Run != nil {
				reserved := false
				d.mu.Lock()
				if d.claimMode {
					if m.ClaimID == "" || m.ClaimID != d.claimID {
						d.mu.Unlock()
						ws.CloseNow()
						return errors.New("stale task claim")
					}
					d.claimID = ""
					reserved = true
				}
				d.mu.Unlock()
				d.startMessage(*m.Run, reserved)
			}
		case protocol.TypeCancel:
			d.mu.Lock()
			for _, r := range d.running {
				if r.dispatch.RunID == m.RunID && r.dispatch.AttemptID == m.AttemptID {
					r.cancel()
				}
			}
			d.mu.Unlock()
		case protocol.TypeSteer:
			// 插话：交给正在跑这条消息的 harness;不在跑就忽略，服务会把它转成下一条消息
			if m.Steer != nil {
				received := false
				d.mu.Lock()
				if r := d.running[executionKey(m.Steer.MessageID, m.Steer.RunID, m.Steer.AttemptID)]; r != nil && r.dispatch.RunID == m.Steer.RunID && r.dispatch.AttemptID == m.Steer.AttemptID {
					received = r.in.Steer(*m.Steer)
				}
				d.mu.Unlock()
				if received {
					d.recordSteer(*m.Steer)
				}
			}
		case protocol.TypeAnswer:
			// 提问的答案：交给正在等的 harness;已经不在等就忽略
			if m.Answer != nil {
				d.mu.Lock()
				r := d.running[executionKey(m.Answer.MessageID, m.Answer.RunID, m.Answer.AttemptID)]
				d.mu.Unlock()
				if r != nil && r.dispatch.RunID == m.Answer.RunID && r.dispatch.AttemptID == m.Answer.AttemptID {
					r.in.Answer(*m.Answer)
				}
			}
		case protocol.TypeEventAck:
			if d.cfg.LocalStore != nil {
				if err := d.cfg.LocalStore.Confirm(ctx, d.cfg.EndpointID, m.RunID, m.AttemptID); err != nil {
					d.log.Printf("confirm: %v", err)
				}
			}
		case protocol.TypeError:
			if m.ErrorCode == "stale_attempt" && d.cfg.LocalStore != nil {
				_ = d.cfg.LocalStore.RejectReport(ctx, d.cfg.EndpointID, m.RunID, m.AttemptID, m.Error)
			}
			if m.MessageID != 0 {
				d.mu.Lock()
				r := d.running[executionKey(m.MessageID, m.RunID, m.AttemptID)]
				d.mu.Unlock()
				if r != nil && r.dispatch.RunID == m.RunID && r.dispatch.AttemptID == m.AttemptID {
					d.log.Printf("消息 %d 已不归本机(%s),停止执行", m.MessageID, m.Error)
					r.cancel()
				}
			} else {
				d.log.Printf("服务报错: %s", m.Error)
			}
		}
	}
}

type runningMsg struct {
	done     chan struct{}
	dispatch protocol.Dispatch
	cancel   context.CancelFunc
	in       *harness.Input
	local    store.Session
}

// startMessage 在后台跑 harness 回一条消息，跑完回报结果并再说一次 ready。
// 不跟连接绑定：中途断线重连，结果照样回报给新连接。
func (d *endpointConnection) startMessage(m protocol.Dispatch, reservations ...bool) {
	m.AttachmentSupport = true
	d.mu.Lock()
	la, ok := d.harnesses[m.Harness]
	cfgPath := d.cfg.ConfigPath
	if cfgPath == "" {
		cfgPath = endpoint.DefaultPath()
	}
	la.Env = append(workEnv(d.cfg.Server, m), "AGENTUSE_CONFIG="+cfgPath, "AGENTUSE_TOKEN="+d.cfg.Key)
	tctx, cancel := context.WithCancel(context.Background())
	in := harness.NewInput(d.cfg.askTimeout())
	r := &runningMsg{cancel: cancel, in: in, dispatch: m, done: make(chan struct{})}
	d.running[executionKey(m.ID, m.RunID, m.AttemptID)] = r
	d.mu.Unlock()
	var reportMu sync.Mutex
	var eventSeq int64
	report := func(kind string, p any) {
		reportMu.Lock()
		defer reportMu.Unlock()
		eventSeq++
		b, _ := json.Marshal(p)
		if d.cfg.LocalStore != nil && r.local.ID != "" {
			if err := d.cfg.LocalStore.Event(tctx, d.cfg.EndpointID, m.RunID, m.AttemptID, kind, b); err != nil {
				d.log.Printf("record event: %v", err)
			}
		}
		event := protocol.Message{Type: protocol.TypeEvent, MessageID: m.ID, RunID: m.RunID, AttemptID: m.AttemptID, EventSeq: eventSeq, Kind: kind, Payload: b}
		if (kind == "done" || kind == "failed") && d.cfg.LocalStore != nil && r.local.ID != "" {
			raw, _ := json.Marshal(event)
			if e := d.cfg.LocalStore.PrepareReport(tctx, d.cfg.EndpointID, m.RunID, m.AttemptID, raw); e != nil {
				d.log.Printf("persist result: %v", e)
				return
			}
		}
		_ = d.send(event)
	}
	go func() {
		defer close(r.done)
		if len(reservations) > 0 && reservations[0] {
			defer func() { <-d.cfg.Slots }()
		}
		defer func() {
			d.mu.Lock()
			delete(d.running, executionKey(m.ID, m.RunID, m.AttemptID))
			d.mu.Unlock()
			cancel()
			if !d.isPaused() {
				_ = d.send(protocol.Message{Type: protocol.TypeReady})
			}
		}()
		d.log.Printf("会话 %s(%s)开始回消息 %d", m.SessionID, m.Harness, m.ID)
		var res harness.Result
		recorded := false
		defer func() {
			if r.local.ID != "" {
				if !recorded {
					d.recordCompleteLocal(m, r.local, res, ok, tctx.Err())
				}
				seq := int64(0)
				if res.OK {
					seq = m.ContextSeq
				}
				if !res.CleanupPending {
					if err := d.cfg.LocalStore.ReleaseSession(r.local, seq, res.OK || errors.Is(tctx.Err(), context.Canceled), m.RunID, m.AttemptID); err != nil {
						d.log.Printf("save session: %v", err)
					}
				}
			}
		}()
		var dir string
		var prepareErr error
		if ok && d.cfg.LocalStore != nil {
			if d.cfg.Sessions != nil {
				r.local, dir, prepareErr = d.cfg.Sessions.Acquire(tctx, d.cfg.EndpointID, m.SessionID, m.AgentID, m.Harness, la.Revision)
			} else if q := d.cfg.Settings.Session.Quota; q != nil {
				_, prepareErr = d.cfg.LocalStore.Reclaim(tctx, q.MaxBytes)
			}
			if prepareErr == nil {
				if r.local.ID == "" {
					r.local, dir, prepareErr = d.cfg.LocalStore.AcquireSession(tctx, d.cfg.EndpointID, m.SessionID, m.AgentID, m.Harness, la.Revision)
				}
				la.EventsPath = harness.EventsPath(dir, r.local.ID)
				if prepareErr == nil {
					d.recordReceivedLocal(m, r.local, dir)
				}
			}
		} else if ok {
			dir, prepareErr = d.sessionDir(m)
		}
		if !ok {
			res = harness.Result{Error: "本机没有 harness " + m.Harness}
		} else if prepareErr != nil {
			res = harness.Result{Error: prepareErr.Error()}
		} else if err := d.loadRunHistory(tctx, &m); err != nil {
			res = harness.Result{Error: "共享历史读取失败: " + err.Error()}
		} else if err := d.syncAttachments(tctx, &m, dir); err != nil {
			res = harness.Result{Error: "附件同步失败: " + err.Error()}
		} else if err := d.prepareHistory(&m, r.local, &la, dir); err != nil {
			res = harness.Result{Error: err.Error()}
		} else {
			if m.RunID != "" {
				report("started", map[string]any{"task_message_ids": m.TaskMessageIDs, "context_seq": m.ContextSeq})
			}
			res = d.cfg.Harness.Execute(tctx, la, m, dir, in, report)
		}
		if tctx.Err() != nil {
			if d.cfg.LocalStore != nil && r.local.ID != "" {
				_ = d.cfg.LocalStore.Confirm(context.Background(), d.cfg.EndpointID, m.RunID, m.AttemptID)
			}
			d.log.Printf("消息 %d 已取消", m.ID)
			return
		}
		kind := "done"
		if !res.OK {
			kind = "failed"
		}
		if r.local.ID != "" {
			d.recordCompleteLocal(m, r.local, res, ok, nil)
			recorded = true
		}
		report(kind, res)
		d.log.Printf("消息 %d 结束: %s", m.ID, kind)
	}()
}

// sessionDir 是会话的工作目录，固定为 ~/.agentuse/work/<session_id>,每条消息都在同一个目录。
func (d *endpointConnection) sessionDir(m protocol.Dispatch) (string, error) {
	if m.SessionID == "" || strings.ContainsAny(m.SessionID, `/\`) || strings.HasPrefix(m.SessionID, ".") {
		return "", errors.New("会话 id 不合法: " + m.SessionID)
	}
	dir, err := store.SafePath(d.workdir, d.cfg.EndpointID, m.SessionID, "workspace")
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0o700)
}

func (d *endpointConnection) isPaused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.claimMode
}

func executionKey(message int64, run, attempt string) string {
	if run != "" {
		return run + "/" + attempt
	}
	return "message/" + strconv.FormatInt(message, 10)
}
