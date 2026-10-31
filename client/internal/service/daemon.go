package service

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
)

// Version 由 main 设置，hello 时上报。
var Version = "dev"

type Daemon struct {
	cfg     Config
	log     *log.Logger
	workdir string // 会话目录的根:~/.agentuse/work

	mu        sync.Mutex
	harnesses map[string]localHarness
	running   map[int64]*runningMsg        // 正在回的消息(一次最多一条)
	conn      func(protocol.Message) error // 当前连接的发送函数，断线时为 nil
	outbox    []protocol.Message           // 断线期间跑完的结果，重连后补发
}

var errOffline = errors.New("未连接")

// send 发到当前连接。断线时进度直接丢掉，最终结果留到重连后补发。
func (d *Daemon) send(m protocol.Message) error {
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

func New(cfg Config, logger *log.Logger) (*Daemon, error) {
	if cfg.Server == "" || cfg.Key == "" {
		return nil, errors.New("daemon 还没注册：请先运行页面上的安装命令(或 agentuse register)")
	}
	if logger == nil {
		logger = log.Default()
	}
	home, _ := os.UserHomeDir()
	return &Daemon{cfg: cfg, log: logger, workdir: filepath.Join(home, ".agentuse", "work"), running: map[int64]*runningMsg{}}, nil
}

// Run 连上服务干活，断线自动退避重连，直到 ctx 结束。
func (d *Daemon) Run(ctx context.Context) error {
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

var errUnauthorized = errors.New("daemon 密钥无效(可能在页面上被删除或重置了),请重新运行安装命令")

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
func (d *Daemon) session(ctx context.Context) error {
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
	d.harnesses = detectHarnesses(d.cfg)
	hello := protocol.Message{Type: protocol.TypeHello, Version: Version, Capabilities: []string{attachment.Capability, "session-runs-v1", "codex-steer-v1"}}
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
		d.mu.Unlock()
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
				for id, r := range d.running {
					beats = append(beats, protocol.Message{Type: protocol.TypeHeartbeat, MessageID: id, RunID: r.dispatch.RunID, AttemptID: r.dispatch.AttemptID})
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
			if !busy {
				if err := send(protocol.Message{Type: protocol.TypeReady}); err != nil {
					return err
				}
			}
		case protocol.TypeRun:
			if m.Run != nil {
				d.startMessage(*m.Run)
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
				d.mu.Lock()
				if r := d.running[m.Steer.MessageID]; r != nil && r.dispatch.RunID == m.Steer.RunID && r.dispatch.AttemptID == m.Steer.AttemptID {
					r.in.steers.push(*m.Steer)
				}
				d.mu.Unlock()
			}
		case protocol.TypeAnswer:
			// 提问的答案：交给正在等的 harness;已经不在等就忽略
			if m.Answer != nil {
				d.mu.Lock()
				r := d.running[m.Answer.MessageID]
				d.mu.Unlock()
				if r != nil && r.dispatch.RunID == m.Answer.RunID && r.dispatch.AttemptID == m.Answer.AttemptID {
					r.in.asks.answer(*m.Answer)
				}
			}
		case protocol.TypeError:
			if m.MessageID != 0 {
				d.mu.Lock()
				r := d.running[m.MessageID]
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
	in       *inbox
}

// startMessage 在后台跑 harness 回一条消息，跑完回报结果并再说一次 ready。
// 不跟连接绑定：中途断线重连，结果照样回报给新连接。
func (d *Daemon) startMessage(m protocol.Dispatch) {
	m.AttachmentSupport = true
	d.mu.Lock()
	la, ok := d.harnesses[m.Harness]
	cfgPath := d.cfg.ConfigPath
	if cfgPath == "" {
		cfgPath = DefaultConfigPath()
	}
	la.Env = append(workEnv(d.cfg.Server, m), "AGENTUSE_CONFIG="+cfgPath)
	tctx, cancel := context.WithCancel(context.Background())
	in := &inbox{steers: &steerQueue{}, asks: newAskBox(d.cfg.askTimeout())}
	r := &runningMsg{cancel: cancel, in: in, dispatch: m, done: make(chan struct{})}
	d.running[m.ID] = r
	d.mu.Unlock()
	var reportMu sync.Mutex
	var eventSeq int64
	report := func(kind string, p any) {
		reportMu.Lock()
		defer reportMu.Unlock()
		eventSeq++
		b, _ := json.Marshal(p)
		_ = d.send(protocol.Message{Type: protocol.TypeEvent, MessageID: m.ID, RunID: m.RunID, AttemptID: m.AttemptID, EventSeq: eventSeq, Kind: kind, Payload: b})
	}
	go func() {
		defer close(r.done)
		defer func() {
			d.mu.Lock()
			delete(d.running, m.ID)
			d.mu.Unlock()
			cancel()
			_ = d.send(protocol.Message{Type: protocol.TypeReady})
		}()
		d.log.Printf("会话 %s(%s)开始回消息 %d", m.SessionID, m.Harness, m.ID)
		var res Result
		if !ok {
			res = Result{Error: "本机没有 harness " + m.Harness}
		} else if dir, err := d.sessionDir(m); err != nil {
			res = Result{Error: err.Error()}
		} else if err := d.loadRunHistory(tctx, &m); err != nil {
			res = Result{Error: "共享历史读取失败: " + err.Error()}
		} else if err := d.syncAttachments(tctx, &m, dir); err != nil {
			res = Result{Error: "附件同步失败: " + err.Error()}
		} else if err := writeSessionHistory(m, dir); err != nil {
			res = Result{Error: err.Error()}
		} else {
			if m.RunID != "" {
				report("started", map[string]any{"task_message_ids": m.TaskMessageIDs, "context_seq": m.ContextSeq})
			}
			res = runHarness(tctx, la, m, dir, in, report)
		}
		if tctx.Err() != nil {
			d.log.Printf("消息 %d 已取消", m.ID)
			return
		}
		kind := "done"
		if !res.OK {
			kind = "failed"
		}
		report(kind, res)
		d.log.Printf("消息 %d 结束: %s", m.ID, kind)
	}()
}

// sessionDir 是会话的工作目录，固定为 ~/.agentuse/work/<session_id>,每条消息都在同一个目录。
func (d *Daemon) sessionDir(m protocol.Dispatch) (string, error) {
	if m.SessionID == "" || strings.ContainsAny(m.SessionID, `/\`) || strings.HasPrefix(m.SessionID, ".") {
		return "", errors.New("会话 id 不合法: " + m.SessionID)
	}
	dir := filepath.Join(d.workdir, m.SessionID)
	return dir, os.MkdirAll(dir, 0o755)
}
