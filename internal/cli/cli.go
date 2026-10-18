// Package cli 是 agentuse 命令行。三类使用者:
//
//	实例主人:agentuse hub start;查看 agentuse admin sessions / runs / run <id>
//	用户电脑:agentuse connect <hub> <接入码>;agentuse daemon start;agentuse agents / disconnect
//	agent 自己(带 AGENTUSE_TOKEN):agentuse reply / status / activity / run
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/hub"
	"github.com/memory-co/agentuse/internal/services/callback"
	"github.com/memory-co/agentuse/internal/services/token"
	"github.com/memory-co/agentuse/internal/store"
)

// Version 在构建时用 -ldflags "-X github.com/memory-co/agentuse/internal/cli.Version=…" 覆盖。
var Version = "dev"

const usage = `agentuse —— 让任意平台的评论区 @ 到用户自己电脑上的 agent

用法:
  agentuse hub start    [-config ~/.agentuse/hub.json]      起 hub 实例
  agentuse connect <hub 地址> <接入码> [-dir .] [-base-ref 当前分支]   把这台电脑接成你的 agent
  agentuse daemon start [-config ~/.agentuse/daemon.json]   起用户电脑上的 daemon
  agentuse agents                                           看这台电脑上接了哪些 agent
  agentuse disconnect <agent>                               把一个 agent 从这台电脑上解绑
  agentuse reply  <文本|->                                  (agent 用)回复评论
  agentuse status <状态>                                    (agent 用)改线程对象的状态
  agentuse activity <thought|action|elicitation|response|error> <文本>   (agent 用)报进度
  agentuse run                                              (agent 用)看这次运行的令牌范围
  agentuse admin sessions|runs|run <id> [-platform -status -session -agent -limit]   (实例主人)查看会话 / 运行
  agentuse version
`

// Main 是入口,返回进程退出码。
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "hub":
		err = sub(args[1:], "start", hubStart)
	case "daemon":
		err = sub(args[1:], "start", daemonStart)
	case "connect":
		err = connect(args[1:])
	case "disconnect":
		err = disconnect(args[1:])
	case "agents":
		err = agents(args[1:])
	case "reply":
		err = reply(args[1:])
	case "status":
		err = status(args[1:])
	case "activity":
		err = activity(args[1:])
	case "run":
		err = runInfo()
	case "admin":
		err = admin(args[1:])
	case "version":
		fmt.Println(Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentuse:", err)
		return 1
	}
	return 0
}

func sub(args []string, name string, fn func([]string) error) error {
	if len(args) == 0 || args[0] != name {
		return fmt.Errorf("expected %q", name)
	}
	return fn(args[1:])
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func hubStart(args []string) error {
	fs := flag.NewFlagSet("hub start", flag.ContinueOnError)
	path := fs.String("config", filepath.Join(config.Home(), "hub.json"), "hub 配置文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadHub(*path)
	if err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	h := hub.New(cfg.Platforms, st, token.NewIssuer([]byte(cfg.TokenKey)), callback.NewHTTPSender(), hub.Options{
		LeaseTTL:    time.Duration(cfg.LeaseSeconds) * time.Second,
		MaxAttempts: cfg.MaxAttempts,
		PublicURL:   cfg.PublicURL,
	})
	handler := hub.NewServer(h, hub.ServerConfig{AdminKey: cfg.AdminKey}).Handler()
	srv := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signalContext()
	defer stop()
	go h.RunReaper(ctx, 10*time.Second)
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	fmt.Fprintf(os.Stderr, "agentuse hub listening on %s (%d platforms, store %s)\n", cfg.Listen, len(cfg.Platforms), cfg.Store)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openStore(cfg config.Hub) (store.Store, error) {
	switch cfg.Store {
	case "memory":
		return store.NewMemory(), nil
	case "sqlite":
		if err := os.MkdirAll(filepath.Dir(cfg.DB), 0o700); err != nil {
			return nil, err
		}
		return store.OpenSQLite(cfg.DB)
	default:
		return nil, fmt.Errorf("unknown store %q (sqlite / memory)", cfg.Store)
	}
}

func daemonConfigPath() string { return filepath.Join(config.Home(), "daemon.json") }

func daemonStart(args []string) error {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	path := fs.String("config", daemonConfigPath(), "daemon 配置文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadDaemon(*path)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	// daemon.json 里每个 (hub, 平台) 一条循环,并行跑。
	errs := make(chan error, len(cfg.Hubs))
	for _, conn := range cfg.Hubs {
		d, err := daemon.New(conn, cfg, config.Home())
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "agentuse daemon %s → %s [%s] (%s)\n", conn.DeviceID, conn.URL, conn.Platform, transportName(cfg.Transport))
		go func() { errs <- d.Run(ctx) }()
	}
	var first error
	for range cfg.Hubs {
		if err := <-errs; err != nil && !errors.Is(err, context.Canceled) && first == nil {
			first = err
			stop()
		}
	}
	return first
}

func transportName(t string) string {
	if t == "" {
		return "ws"
	}
	return t
}

// admin 是实例主人的只读查询:AGENTUSE_URL + AGENTUSE_ADMIN_KEY(或 -hub / -key)。
func admin(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agentuse admin sessions|runs|run <id>")
	}
	fs := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
	base := fs.String("hub", os.Getenv("AGENTUSE_URL"), "hub 地址")
	key := fs.String("key", os.Getenv("AGENTUSE_ADMIN_KEY"), "管理密钥(hub.json 的 admin_key)")
	platform := fs.String("platform", "", "只看这个平台")
	status := fs.String("status", "", "只看这个状态(runs)")
	session := fs.String("session", "", "只看这个会话(runs)")
	agent := fs.String("agent", "", "只看这个 agent 的(runs,ag_…)")
	limit := fs.Int("limit", 0, "最多几条(默认 100)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *base == "" || *key == "" {
		return errors.New("need -hub and -key (or AGENTUSE_URL / AGENTUSE_ADMIN_KEY)")
	}
	q := url.Values{}
	for k, v := range map[string]string{"platform": *platform, "status": *status, "session": *session, "agent": *agent} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if *limit > 0 {
		q.Set("limit", strconv.Itoa(*limit))
	}
	var path string
	switch args[0] {
	case "sessions":
		path = "/v1/admin/sessions"
	case "runs":
		path = "/v1/admin/runs"
	case "run":
		if fs.NArg() != 1 {
			return errors.New("usage: agentuse admin run <id>")
		}
		path = "/v1/admin/runs/" + url.PathEscape(fs.Arg(0))
	default:
		return fmt.Errorf("unknown admin command %q", args[0])
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out json.RawMessage
	if err := call(*base, *key, http.MethodGet, path, nil, &out); err != nil {
		return err
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") != nil {
		pretty.Write(out)
	}
	fmt.Println(pretty.String())
	return nil
}

// parseMixed 解析 flag 和位置参数混排的命令行(agentuse connect <hub> <码> -dir x)。
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func reply(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: agentuse reply <文本|->")
	}
	text := args[0]
	if text == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	return agentCall(http.MethodPost, "/v1/run/reply", map[string]any{"text": text}, nil)
}

func status(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: agentuse status <状态>")
	}
	return agentCall(http.MethodPost, "/v1/run/status", map[string]any{"status": args[0]}, nil)
}

func activity(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: agentuse activity <type> <文本>")
	}
	return agentCall(http.MethodPost, "/v1/run/activity", map[string]any{"activity": map[string]string{"type": args[0], "body": args[1]}}, nil)
}

func runInfo() error {
	var out json.RawMessage
	if err := agentCall(http.MethodGet, "/v1/run", nil, &out); err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// agentCall 用环境变量里的运行令牌调 hub:AGENTUSE_URL + AGENTUSE_TOKEN,由 daemon 注入。
func agentCall(method, path string, in, out any) error {
	base, tok := os.Getenv("AGENTUSE_URL"), os.Getenv("AGENTUSE_TOKEN")
	if base == "" || tok == "" {
		return errors.New("AGENTUSE_URL / AGENTUSE_TOKEN not set (only available inside a run)")
	}
	return call(base, tok, method, path, in, out)
}

func call(base, tok, method, path string, in, out any) error {
	var body io.Reader = http.NoBody
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
