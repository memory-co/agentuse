// Package cli 是 agentuse 的命令行入口。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/server"
	"github.com/memory-co/agentuse/internal/store"
)

// Version 由构建时 -ldflags 注入。
var Version = "dev"

const usage = `agentuse —— 让你电脑上的 CLI agent 从 agentuse 服务领任务

用法:
  agentuse server [-addr :8080] [-db 路径] [-public-url URL] [-dist 目录]
  agentuse daemon register -server URL -token 安装令牌
  agentuse daemon run
  agentuse user create 用户名 [-role user|admin]
  agentuse user token 用户名          # 重置登录令牌
  agentuse version
`

func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "server":
		err = runServer(args[1:])
	case "daemon":
		err = runDaemon(args[1:])
	case "user":
		err = runUser(args[1:])
	case "version", "-v", "--version":
		fmt.Println(Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 2
		}
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	return 0
}

func defaultDB() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "server.db")
}

func openDB(path string) (*store.Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	addr := fs.String("addr", envOr("AGENTUSE_ADDR", ":8080"), "监听地址")
	dbPath := fs.String("db", envOr("AGENTUSE_DB", defaultDB()), "SQLite 数据库文件")
	publicURL := fs.String("public-url", os.Getenv("AGENTUSE_PUBLIC_URL"), "用户电脑访问服务的地址(写进安装命令),空 = 按请求推断")
	dist := fs.String("dist", envOr("AGENTUSE_DIST", "dist"), "各平台 agentuse 二进制所在目录(agentuse-<os>-<arch>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx, stop := signalContext()
	defer stop()
	if err := st.MarkAllDaemonsOffline(ctx); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	base := strings.TrimRight(*publicURL, "/")
	if base == "" {
		base = "http://" + displayAddr(ln.Addr())
	}
	if n, err := st.CountUsers(ctx); err != nil {
		return err
	} else if n == 0 {
		u, token, err := st.CreateUser(ctx, "admin", store.RoleAdmin)
		if err != nil {
			return err
		}
		log.Printf("第一次启动，已创建管理员 %s。登录令牌(只显示这一次): %s", u.Name, token)
		log.Printf("登录链接: %s/login?token=%s", base, token)
		log.Printf("令牌丢了可以运行: agentuse user token admin -db %s", *dbPath)
	}

	srv := server.New(st, server.Config{PublicURL: *publicURL, DistDir: *dist})
	go srv.Run(ctx)
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Printf("agentuse 服务已启动: %s (数据库 %s)", base, *dbPath)
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func displayAddr(a net.Addr) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func runDaemon(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: agentuse daemon register|run")
	}
	fs := flag.NewFlagSet("daemon "+args[0], flag.ContinueOnError)
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	switch args[0] {
	case "register":
		srvURL := fs.String("server", "", "agentuse 服务地址")
		token := fs.String("token", os.Getenv("TOKEN"), "页面上生成的安装令牌")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *srvURL == "" || *token == "" {
			return errors.New("需要 -server 和 -token")
		}
		cfg, err := daemon.Register(context.Background(), *srvURL, *token, *cfgPath)
		if err != nil {
			return fmt.Errorf("注册失败: %w", err)
		}
		fmt.Printf("注册成功: daemon %s,配置已写入 %s\n", cfg.DaemonID, *cfgPath)
		return nil
	case "run":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := daemon.LoadConfig(*cfgPath)
		if err != nil {
			return fmt.Errorf("读取配置失败(先运行安装命令注册): %w", err)
		}
		daemon.Version = Version
		d, err := daemon.New(cfg, nil)
		if err != nil {
			return err
		}
		ctx, stop := signalContext()
		defer stop()
		if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	return fmt.Errorf("未知命令 daemon %s", args[0])
}

func runUser(args []string) error {
	if len(args) < 2 {
		return errors.New("用法: agentuse user create|token 用户名")
	}
	fs := flag.NewFlagSet("user "+args[0], flag.ContinueOnError)
	dbPath := fs.String("db", envOr("AGENTUSE_DB", defaultDB()), "SQLite 数据库文件")
	role := fs.String("role", store.RoleUser, "角色: user / admin(仅 create)")
	name := args[1]
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	st, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	switch args[0] {
	case "create":
		u, token, err := st.CreateUser(ctx, name, *role)
		if err != nil {
			return err
		}
		fmt.Printf("已创建用户 %s(%s),登录令牌: %s\n", u.Name, u.Role, token)
	case "token":
		token, err := st.ResetUserToken(ctx, name)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("没有用户 %s", name)
		}
		if err != nil {
			return err
		}
		fmt.Printf("%s 的新登录令牌: %s(旧令牌已失效)\n", name, token)
	default:
		return fmt.Errorf("未知命令 user %s", args[0])
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
