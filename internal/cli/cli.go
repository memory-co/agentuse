// Package cli 是 agentuse 命令行。三类使用者:
//
//	实例主人:agentuse hub start
//	用户电脑:agentuse daemon start
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
	"os"
	"os/signal"
	"path/filepath"
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
  agentuse daemon start [-config ~/.agentuse/daemon.json]   起用户电脑上的 daemon
  agentuse reply  <文本|->                                  (agent 用)回复评论
  agentuse status <状态>                                    (agent 用)改线程对象的状态
  agentuse activity <thought|action|elicitation|response|error> <文本>   (agent 用)报进度
  agentuse run                                              (agent 用)看这次运行的令牌范围
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
	case "reply":
		err = reply(args[1:])
	case "status":
		err = status(args[1:])
	case "activity":
		err = activity(args[1:])
	case "run":
		err = runInfo()
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
	h := hub.New(cfg.Platforms, store.NewMemory(), token.NewIssuer([]byte(cfg.TokenKey)), callback.NewHTTPSender())
	srv := &http.Server{Addr: cfg.Listen, Handler: hub.NewServer(h, cfg.DaemonKeys).Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signalContext()
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	fmt.Fprintf(os.Stderr, "agentuse hub listening on %s (%d platforms)\n", cfg.Listen, len(cfg.Platforms))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func daemonStart(args []string) error {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	path := fs.String("config", filepath.Join(config.Home(), "daemon.json"), "daemon 配置文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadDaemon(*path)
	if err != nil {
		return err
	}
	d, err := daemon.New(cfg, config.Home())
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	fmt.Fprintf(os.Stderr, "agentuse daemon %s → %s\n", cfg.ID, cfg.HubURL)
	if err := d.Run(ctx); !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
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
