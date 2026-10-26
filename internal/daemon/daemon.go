package daemon

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
	running   map[int64]context.CancelFunc // 正在回的消息(一次最多一条)
	conn      func(protocol.Message) error // 当前连接的发送函数，断线时为 nil
	outbox    []protocol.Message           // 断线期间跑完的结果，重连后补发
}

var errOffline = errors.New("未连接")

// send 发到当前连接。断线时进度直接丢掉，最终结果留到重连后补发。
func (d *Daemon) send(m protocol.Message) error {
	d.mu.Lock()
	conn := d.conn
	if conn == nil && m.Type == protocol.TypeEvent && (m.Kind == "done" || m.Kind == "failed") {
		d.outbox = append(d.outbox, m)
	}
	d.mu.Unlock()
	if conn == nil {
		return errOffline
	}
	return conn(m)
}

func New(cfg Config, logger *log.Logger) (*Daemon, error) {
	if cfg.Server == "" || cfg.Key == "" {
		return nil, errors.New("daemon 还没注册：请先运行页面上的安装命令(或 agentuse register)")
	}
	if logger == nil {
		logger = log.Default()
	}
	home, _ := os.UserHomeDir()
	return &Daemon{cfg: cfg, log: logger, workdir: filepath.Join(home, ".agentuse", "work"), running: map[int64]context.CancelFunc{}}, nil
}

// Run 连上服务干活，断线自动退避重连，直到 ctx 结束。
func (d *Daemon) Run(ctx context.Context) error {
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
	hello := protocol.Message{Type: protocol.TypeHello, Version: Version}
	for _, a := range d.harnesses {
		hello.Harnesses = append(hello.Harnesses, protocol.Harness{Name: a.Name, Version: a.Version})
	}
	sort.Slice(hello.Harnesses, func(i, j int) bool { return hello.Harnesses[i].Name < hello.Harnesses[j].Name })
	for id := range d.running {
		hello.Running = append(hello.Running, id)
	}
	for _, m := range d.outbox {
		hello.Running = append(hello.Running, m.MessageID)
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
				ids := make([]int64, 0, len(d.running))
				for id := range d.running {
					ids = append(ids, id)
				}
				d.mu.Unlock()
				if len(ids) == 0 {
					ids = []int64{0}
				}
				for _, id := range ids {
					_ = send(protocol.Message{Type: protocol.TypeHeartbeat, MessageID: id})
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
			outbox := d.outbox
			d.outbox = nil
			busy := len(d.running) > 0
			d.mu.Unlock()
			for _, m := range outbox {
				if err := send(m); err != nil {
					return err
				}
			}
			if !busy {
				if err := send(protocol.Message{Type: protocol.TypeReady}); err != nil {
					return err
				}
			}
		case protocol.TypeMessage:
			if m.Message != nil {
				d.startMessage(*m.Message)
			}
		case protocol.TypeError:
			if m.MessageID != 0 {
				d.mu.Lock()
				cancel := d.running[m.MessageID]
				d.mu.Unlock()
				if cancel != nil {
					d.log.Printf("消息 %d 已不归本机(%s),停止执行", m.MessageID, m.Error)
					cancel()
				}
			} else {
				d.log.Printf("服务报错: %s", m.Error)
			}
		}
	}
}

// startMessage 在后台跑 harness 回一条消息，跑完回报结果并再说一次 ready。
// 不跟连接绑定：中途断线重连，结果照样回报给新连接。
func (d *Daemon) startMessage(m protocol.Dispatch) {
	d.mu.Lock()
	la, ok := d.harnesses[m.Harness]
	tctx, cancel := context.WithCancel(context.Background())
	d.running[m.ID] = cancel
	d.mu.Unlock()

	go func() {
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
		} else {
			res = runHarness(tctx, la, m, dir, func(kind string, p any) {
				b, _ := json.Marshal(p)
				_ = d.send(protocol.Message{Type: protocol.TypeEvent, MessageID: m.ID, Kind: kind, Payload: b})
			})
		}
		if tctx.Err() != nil {
			d.log.Printf("消息 %d 已取消", m.ID)
			return
		}
		kind := "done"
		if !res.OK {
			kind = "failed"
		}
		b, _ := json.Marshal(res)
		if err := d.send(protocol.Message{Type: protocol.TypeEvent, MessageID: m.ID, Kind: kind, Payload: b}); err != nil {
			d.log.Printf("回报消息 %d 结果失败: %v", m.ID, err)
		}
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
