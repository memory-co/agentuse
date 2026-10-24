package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/server"
	"github.com/memory-co/agentuse/internal/store"
)

const serverUsage = `agentuse-server —— agentuse 服务端

用法:
  agentuse-server start [-addr :8080] [-db 路径] [-public-url URL] [-dist 目录] [-reset-admin-token]
  agentuse-server login -server URL -token 管理员令牌
  agentuse-server user create 用户名 [-id 用户id] [-role user|admin]
  agentuse-server user list [-json]
  agentuse-server user token 用户id
  agentuse-server version

start 是服务进程，只有它读写数据库。其余是管理命令，带管理员令牌调 HTTP API,
服务地址和令牌按 -server/-token 参数 → AGENTUSE_SERVER/AGENTUSE_TOKEN → login 存的配置 的顺序找。
`

// ServerMain 是服务端的 agentuse-server。
func ServerMain(args []string) int {
	return dispatch("agentuse-server", serverUsage, args, []command{
		{"start", runStart},
		{"login", runLogin},
		{"user", runUser},
	})
}

func defaultDB() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "server.db")
}

func runStart(args []string) error {
	fs := newFlags("agentuse-server start")
	addr := fs.String("addr", envOr("AGENTUSE_ADDR", ":8080"), "监听地址")
	dbPath := fs.String("db", envOr("AGENTUSE_DB", defaultDB()), "SQLite 数据库文件")
	publicURL := fs.String("public-url", os.Getenv("AGENTUSE_PUBLIC_URL"), "用户电脑访问服务的地址(写进安装命令),空 = 按请求推断")
	dist := fs.String("dist", envOr("AGENTUSE_DIST", "dist"), "各平台 agentuse 二进制所在目录(agentuse-<os>-<arch>)")
	resetAdmin := fs.Bool("reset-admin-token", false, "启动时给管理员换一个新令牌并打印(令牌丢了用)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
			return err
		}
	}
	st, err := store.Open(*dbPath)
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
	if err := bootstrapAdmin(ctx, st, base, *resetAdmin); err != nil {
		return err
	}
	if missing := server.MissingDist(*dist); len(missing) > 0 {
		log.Printf("警告: %s 里缺这些平台的 agentuse 二进制: %s;这些平台的电脑跑 setup.sh 会下载失败(先 make dist)",
			*dist, strings.Join(missing, ", "))
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

// bootstrapAdmin:库里没用户就建 admin;要求重置就给最早的管理员换令牌。都会打印登录链接。
func bootstrapAdmin(ctx context.Context, st *store.Store, base string, reset bool) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	var u store.User
	var token string
	switch {
	case n == 0:
		if u, token, err = st.CreateUser(ctx, "", "admin", store.RoleAdmin); err != nil {
			return err
		}
		log.Printf("第一次启动，已创建管理员 %s(id %s)", u.Name, u.ID)
	case reset:
		admin, err := st.FirstAdmin(ctx)
		if err != nil {
			return fmt.Errorf("找不到管理员: %w", err)
		}
		if u, token, err = st.ResetUserToken(ctx, admin.ID); err != nil {
			return err
		}
		log.Printf("已重置管理员 %s(id %s)的令牌，旧令牌已失效", u.Name, u.ID)
	default:
		return nil
	}
	log.Printf("管理员登录令牌(只显示这一次): %s", token)
	log.Printf("登录链接: %s/login?token=%s", base, token)
	log.Printf("令牌丢了: agentuse-server start -reset-admin-token")
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

func runLogin(args []string) error {
	fs := newFlags("agentuse-server login")
	srvURL := fs.String("server", os.Getenv("AGENTUSE_SERVER"), "服务地址")
	token := fs.String("token", os.Getenv("AGENTUSE_TOKEN"), "管理员登录令牌(ut_ 开头)")
	path := fs.String("config", defaultClientConfig(), "保存到哪")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *srvURL == "" || *token == "" {
		return usageErr("需要 -server 和 -token")
	}
	c := &apiClient{server: strings.TrimRight(*srvURL, "/"), token: *token}
	me, err := c.me(context.Background())
	if err != nil {
		return err
	}
	if me.Role != store.RoleAdmin {
		return fmt.Errorf("%s 不是管理员，管理命令需要管理员令牌", me.Name)
	}
	if err := saveClientConfig(*path, clientConfig{Server: c.server, Token: c.token}); err != nil {
		return err
	}
	fmt.Printf("已登录 %s(管理员 %s),配置已写入 %s\n", c.server, me.Name, *path)
	return nil
}

func runUser(args []string) error {
	if len(args) == 0 {
		return usageErr("用法: agentuse-server user create|list|token")
	}
	switch args[0] {
	case "create":
		return runUserCreate(args[1:])
	case "list":
		return runUserList(args[1:])
	case "token":
		return runUserToken(args[1:])
	}
	return usageErr("未知命令 user %s", args[0])
}

// clientFlags 给管理命令加上 -server / -token / -config。
func clientFlags(name string) (*clientFlagSet, *flagsHolder) {
	fs := newFlags(name)
	h := &flagsHolder{
		server: fs.String("server", "", "服务地址(默认 AGENTUSE_SERVER 或 login 存的配置)"),
		token:  fs.String("token", "", "管理员令牌(默认 AGENTUSE_TOKEN 或 login 存的配置)"),
		config: fs.String("config", defaultClientConfig(), "login 存的配置文件"),
	}
	return &clientFlagSet{fs}, h
}

func runUserCreate(args []string) error {
	// 用户名放在最前面，后面才是参数:user create alice -id u_1
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageErr("用法: agentuse-server user create 用户名 [-id 用户id] [-role user|admin]")
	}
	name := args[0]
	cf, h := clientFlags("agentuse-server user create")
	id := cf.String("id", "", "用户 id,填接入方自己的 user id;不填生成 UUID")
	role := cf.String("role", store.RoleUser, "角色: user / admin")
	if err := parse(cf.FlagSet, args[1:]); err != nil {
		return err
	}
	c, err := h.client()
	if err != nil {
		return err
	}
	res, err := c.createUser(context.Background(), *id, name, *role)
	if err != nil {
		return err
	}
	fmt.Printf("已创建用户 %s(id %s,%s)\n登录令牌(只显示这一次): %s\n登录链接: %s\n",
		res.User.Name, res.User.ID, res.User.Role, res.Token, res.LoginURL)
	return nil
}

func runUserList(args []string) error {
	cf, h := clientFlags("agentuse-server user list")
	asJSON := cf.Bool("json", false, "输出 JSON")
	if err := parse(cf.FlagSet, args); err != nil {
		return err
	}
	c, err := h.client()
	if err != nil {
		return err
	}
	users, err := c.listUsers(context.Background())
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(os.Stdout, users)
	}
	rows := [][]string{{"id", "用户名", "角色", "创建时间"}}
	for _, u := range users {
		rows = append(rows, []string{u.ID, u.Name, u.Role, time.Unix(u.CreatedAt, 0).Format("2006-01-02 15:04")})
	}
	printTable(os.Stdout, rows)
	return nil
}

func runUserToken(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageErr("用法: agentuse-server user token 用户id")
	}
	id := args[0]
	cf, h := clientFlags("agentuse-server user token")
	if err := parse(cf.FlagSet, args[1:]); err != nil {
		return err
	}
	c, err := h.client()
	if err != nil {
		return err
	}
	res, err := c.resetToken(context.Background(), id)
	if err != nil {
		return err
	}
	fmt.Printf("%s(id %s)的新登录令牌: %s(旧令牌已失效)\n登录链接: %s\n", res.User.Name, res.User.ID, res.Token, res.LoginURL)
	return nil
}
