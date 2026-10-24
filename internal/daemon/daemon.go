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
	cfg Config
	log *log.Logger

	mu      sync.Mutex
	agents  map[string]localAgent
	running map[string]context.CancelFunc // 正在跑的任务(一次最多一个)
	conn    func(protocol.Message) error  // 当前连接的发送函数，断线时为 nil
	outbox  []protocol.Message            // 断线期间跑完的任务结果，重连后补发
}

var errOffline = errors.New("未连接")

// send 发到当前连接。断线时进度直接丢掉，最终结果留到重连后补发。
func (d *Daemon) send(m protocol.Message) error {
	d.mu.Lock()
	conn := d.conn
	if conn == nil && m.Type == protocol.TypeEvent && m.Kind != "progress" {
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
	if cfg.Workdir == "" {
		home, _ := os.UserHomeDir()
		cfg.Workdir = filepath.Join(home, ".agentuse", "work")
	}
	cfg.Workdir = expandHome(cfg.Workdir)
	return &Daemon{cfg: cfg, log: logger, running: map[string]context.CancelFunc{}}, nil
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
	d.agents = detectAgents(d.cfg)
	hello := protocol.Message{Type: protocol.TypeHello, Version: Version}
	for _, a := range d.agents {
		hello.Agents = append(hello.Agents, protocol.Agent{Name: a.Name, Version: a.Version})
	}
	sort.Slice(hello.Agents, func(i, j int) bool { return hello.Agents[i].Name < hello.Agents[j].Name })
	for id := range d.running {
		hello.Running = append(hello.Running, id)
	}
	for _, m := range d.outbox {
		hello.Running = append(hello.Running, m.TaskID)
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

	// 心跳:有任务时续租，没任务时保活。
	go func() {
		t := time.NewTicker(protocol.HeartbeatSeconds * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.mu.Lock()
				ids := make([]string, 0, len(d.running))
				for id := range d.running {
					ids = append(ids, id)
				}
				d.mu.Unlock()
				if len(ids) == 0 {
					ids = []string{""}
				}
				for _, id := range ids {
					_ = send(protocol.Message{Type: protocol.TypeHeartbeat, TaskID: id})
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
			names := make([]string, 0, len(hello.Agents))
			for _, a := range hello.Agents {
				names = append(names, a.Name)
			}
			d.log.Printf("已连上 %s,daemon %s(%s),本机 agent: %v", d.cfg.Server, m.DaemonID, m.Name, names)
			if len(m.Ignored) > 0 {
				d.log.Printf("这些 agent 服务上没登记，已忽略: %v", m.Ignored)
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
		case protocol.TypeTask:
			if m.Task != nil {
				d.startTask(*m.Task)
			}
		case protocol.TypeError:
			if m.TaskID != "" {
				d.mu.Lock()
				cancel := d.running[m.TaskID]
				d.mu.Unlock()
				if cancel != nil {
					d.log.Printf("任务 %s 已不归本机(%s),停止执行", m.TaskID, m.Error)
					cancel()
				}
			} else {
				d.log.Printf("服务报错: %s", m.Error)
			}
		}
	}
}

// startTask 在后台跑一个任务，跑完回报结果并再说一次 ready。
// 任务不跟连接绑定：中途断线重连，结果照样回报给新连接。
func (d *Daemon) startTask(t protocol.Task) {
	d.mu.Lock()
	la, ok := d.agents[t.Agent]
	tctx, cancel := context.WithCancel(context.Background())
	d.running[t.ID] = cancel
	d.mu.Unlock()

	go func() {
		defer func() {
			d.mu.Lock()
			delete(d.running, t.ID)
			d.mu.Unlock()
			cancel()
			_ = d.send(protocol.Message{Type: protocol.TypeReady})
		}()
		d.log.Printf("开始任务 %s(%s)", t.ID, t.Agent)
		var res Result
		if !ok {
			res = Result{Error: "本机没有 agent " + t.Agent}
		} else if dir, err := d.taskDir(t); err != nil {
			res = Result{Error: err.Error()}
		} else {
			res = runAgent(tctx, la, t.Input, dir, func(p any) {
				b, _ := json.Marshal(p)
				_ = d.send(protocol.Message{Type: protocol.TypeEvent, TaskID: t.ID, Kind: "progress", Payload: b})
			})
		}
		if tctx.Err() != nil {
			d.log.Printf("任务 %s 已取消", t.ID)
			return
		}
		kind := "done"
		if !res.OK {
			kind = "failed"
		}
		b, _ := json.Marshal(res)
		if err := d.send(protocol.Message{Type: protocol.TypeEvent, TaskID: t.ID, Kind: kind, Payload: b}); err != nil {
			d.log.Printf("回报任务 %s 结果失败: %v", t.ID, err)
		}
		d.log.Printf("任务 %s 结束: %s", t.ID, kind)
	}()
}

func (d *Daemon) taskDir(t protocol.Task) (string, error) {
	var ti protocol.TaskInput
	_ = json.Unmarshal(t.Input, &ti)
	if ti.Cwd != "" {
		dir := expandHome(ti.Cwd)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "", errors.New("工作目录不存在: " + ti.Cwd)
		}
		return dir, nil
	}
	dir := filepath.Join(d.cfg.Workdir, t.ID)
	return dir, os.MkdirAll(dir, 0o755)
}
