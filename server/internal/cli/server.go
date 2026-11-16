package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/command"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	server "github.com/memory-co/agentuse/server/internal/controller"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/hub"
	"github.com/memory-co/agentuse/server/internal/service"
	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
	userservice "github.com/memory-co/agentuse/server/internal/service/user"
)

const serverUsage = `agentuse-server —— agentuse 服务端

用法:
  agentuse-server start [-addr :8080] [-db 路径] [-public-url URL] [-dist 目录] [-reset-admin-token | -admin-token-stdin] [-pid-file 路径]
  agentuse-server login -server URL -token 管理员登录凭据
  agentuse-server user create 用户名 [-id 用户id] [-role user|admin]
  agentuse-server user list [-json]
  agentuse-server user token 用户id
  agentuse-server version

start 是服务进程，只有它读写数据库。其余命令带用户令牌调 HTTP API，用户管理需要管理员权限,
服务地址和令牌按 -server/-token 参数 → AGENTUSE_SERVER/AGENTUSE_TOKEN → login 存的配置 的顺序找。
start 从 ~/.agentuse/settings.yaml 读取 public_url；-public-url 和 AGENTUSE_PUBLIC_URL 可覆盖文件。
`

// Main 是服务端的 agentuse-server。
func Main(args []string) int {
	return command.Dispatch("agentuse-server", serverUsage, args, []command.Command{
		{Name: "start", Run: runStart},
		{Name: "login", Run: runLogin},
		{Name: "user", Run: runUser},
	})
}

func defaultDB() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "server.db")
}

// defaultDist 是 install.sh 放各平台 agentuse 的目录。
func defaultDist() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "dist")
}

func runStart(args []string) error {
	fs := command.NewFlags("agentuse-server start")
	addr := fs.String("addr", command.EnvOr("AGENTUSE_ADDR", ":8080"), "监听地址")
	dbDriver := fs.String("db-driver", command.EnvOr("AGENTUSE_DB_DRIVER", "sqlite"), "元数据数据库：sqlite、mysql 或 postgres")
	dbPath := fs.String("db", command.EnvOr("AGENTUSE_DB", defaultDB()), "SQLite 文件路径，或 MySQL/PostgreSQL DSN")
	publicURL := fs.String("public-url", os.Getenv("AGENTUSE_PUBLIC_URL"), "公开访问地址或反向代理地址；覆盖 ~/.agentuse/settings.yaml 的 public_url")
	dist := fs.String("dist", command.EnvOr("AGENTUSE_DIST", defaultDist()), "各平台 agentuse 二进制所在目录(agentuse-<os>-<arch>)")
	home, _ := os.UserHomeDir()
	filesDir := fs.String("files-dir", command.EnvOr("AGENTUSE_FILES_DIR", filepath.Join(home, ".agentuse", "files")), "fileService 本地存储目录")
	filesBackend := fs.String("files-backend", command.EnvOr("AGENTUSE_FILES_BACKEND", "local"), "fileService 后端: local|s3")
	s3Endpoint := fs.String("s3-endpoint", os.Getenv("AGENTUSE_S3_ENDPOINT"), "S3 兼容服务地址")
	s3Bucket := fs.String("s3-bucket", os.Getenv("AGENTUSE_S3_BUCKET"), "S3 bucket")
	s3Region := fs.String("s3-region", command.EnvOr("AGENTUSE_S3_REGION", "us-east-1"), "S3 region")
	s3Prefix := fs.String("s3-prefix", os.Getenv("AGENTUSE_S3_PREFIX"), "S3 对象前缀")
	envInt := func(key string, def int64) int64 {
		v := os.Getenv(key)
		if v == "" {
			return def
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil {
			return -1
		}
		return n
	}
	maxBytes := fs.Int64("attachment-max-bytes", envInt("AGENTUSE_ATTACHMENT_MAX_BYTES", 100<<20), "单附件字节上限")
	conversationMax := fs.Int64("attachment-conversation-max-bytes", envInt("AGENTUSE_ATTACHMENT_CONVERSATION_MAX_BYTES", 1<<30), "会话附件总字节上限")
	resetAdmin := fs.Bool("reset-admin-token", false, "启动时给管理员换一个新令牌并打印(令牌丢了用)")
	adminTokenStdin := fs.Bool("admin-token-stdin", false, "从标准输入读取管理员登录密码，启动时设置；不打印密码")
	pidFile := fs.String("pid-file", "", "将本进程 PID 保存到指定文件，正常退出时删除")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	settings, err := loadStartSettings(filepath.Join(home, ".agentuse", "settings.yaml"))
	if err != nil {
		return err
	}
	publicURLFlag := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "public-url" {
			publicURLFlag = true
		}
	})
	if !publicURLFlag && os.Getenv("AGENTUSE_PUBLIC_URL") == "" {
		*publicURL = settings.PublicURL
	}
	var adminToken string
	if *adminTokenStdin {
		if *resetAdmin {
			return errors.New("admin-token-stdin 和 reset-admin-token 不能同时使用")
		}
		var err error
		adminToken, err = readAdminToken(os.Stdin)
		if err != nil {
			return err
		}
	}
	var files *fileservice.Service
	var fileErr error
	switch *filesBackend {
	case "local":
		files, fileErr = fileservice.NewLocal(*filesDir)
	case "s3":
		files, fileErr = fileservice.NewS3(fileservice.S3Config{Endpoint: *s3Endpoint, Region: *s3Region, Bucket: *s3Bucket, Prefix: *s3Prefix, AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"), SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY")})
	default:
		return errors.New("files-backend 必须是 local 或 s3")
	}
	if fileErr != nil {
		return fileErr
	}
	if *maxBytes <= 0 || *conversationMax <= 0 || *maxBytes > (1<<63-1)-(1<<20) {
		return errors.New("附件容量必须是有效正整数")
	}

	if *dbDriver == "sqlite" && *dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
			return err
		}
	}
	db, err := database.OpenConfig(database.Config{Driver: *dbDriver, DSN: *dbPath})
	if err != nil {
		return err
	}
	defer db.Close()
	st := service.New(db)
	ctx, stop := command.SignalContext()
	defer stop()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	base := strings.TrimRight(*publicURL, "/")
	if base == "" {
		base = "http://" + displayAddr(ln.Addr())
	}
	if *pidFile != "" {
		if err := os.MkdirAll(filepath.Dir(*pidFile), 0o700); err != nil {
			return err
		}
		pid := []byte(strconv.Itoa(os.Getpid()) + "\n")
		if err := os.WriteFile(*pidFile, pid, 0o600); err != nil {
			return err
		}
		defer func() {
			if saved, err := os.ReadFile(*pidFile); err == nil && string(saved) == string(pid) {
				_ = os.Remove(*pidFile)
			}
		}()
	}
	if err := st.DeviceService.MarkAllDevicesOffline(ctx); err != nil {
		return err
	}
	if err := bootstrapAdmin(ctx, st, base, *resetAdmin, adminToken); err != nil {
		return err
	}
	if missing := server.MissingDist(*dist); len(missing) > 0 {
		log.Printf("警告: %s 里缺这些平台的 agentuse 二进制: %s;这些平台的电脑跑 setup.sh 会下载失败(重跑 install.sh,或 make dist 后用 -dist dist)",
			*dist, strings.Join(missing, ", "))
	}

	svc := st
	transport := hub.New(svc, log.Default())
	svc.SetNotifier(transport)
	srv := server.New(st, server.Config{FileStore: files, ClientHandler: transport, PublicURL: base, DistDir: *dist, AttachmentMaxBytes: *maxBytes, AttachmentConversationMaxBytes: *conversationMax})
	go svc.Run(ctx)
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

// bootstrapAdmin creates/resets the first admin. Chosen credentials are never logged.
func bootstrapAdmin(ctx context.Context, st *service.Service, base string, reset bool, chosenToken string) error {
	if chosenToken != "" {
		if err := userservice.ValidateUserToken(chosenToken); err != nil {
			return err
		}
	}
	n, err := st.UserService.CountUsers(ctx)
	if err != nil {
		return err
	}
	var u agentuse.User
	var token string
	switch {
	case n == 0:
		if u, token, err = st.UserService.CreateUser(ctx, "", "admin", agentuse.RoleAdmin); err != nil {
			return err
		}
		log.Printf("第一次启动，已创建管理员 %s(id %s)", u.Name, u.ID)
	case reset || chosenToken != "":
		admin, err := st.UserService.FirstAdmin(ctx)
		if err != nil {
			return fmt.Errorf("找不到管理员: %w", err)
		}
		if chosenToken != "" {
			u, err = st.UserService.SetUserToken(ctx, admin.ID, chosenToken)
		} else {
			u, token, err = st.UserService.ResetUserToken(ctx, admin.ID)
		}
		if err != nil {
			return err
		}
		log.Printf("已重置管理员 %s(id %s)的令牌，旧令牌已失效", u.Name, u.ID)
	default:
		return nil
	}
	if chosenToken != "" {
		if n == 0 {
			if _, err = st.UserService.SetUserToken(ctx, u.ID, chosenToken); err != nil {
				return err
			}
		}
		log.Printf("已设置管理员登录密码，旧登录凭据已失效；请在 %s 输入新密码登录", base)
		return nil
	}
	log.Printf("管理员登录令牌(只显示这一次): %s", token)
	log.Printf("登录链接: %s/login?token=%s", base, token)
	log.Printf("令牌丢了: agentuse-server start -reset-admin-token")
	return nil
}

func readAdminToken(reader io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 1027))
	if err != nil {
		return "", err
	}
	token := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if err := userservice.ValidateUserToken(token); err != nil {
		return "", err
	}
	return token, nil
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
	fs := command.NewFlags("agentuse-server login")
	srvURL := fs.String("server", os.Getenv("AGENTUSE_SERVER"), "服务地址")
	token := fs.String("token", os.Getenv("AGENTUSE_TOKEN"), "管理员登录密码或令牌")
	path := fs.String("config", defaultClientConfig(), "保存到哪")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	if *srvURL == "" || *token == "" {
		return command.UsageErr("需要 -server 和 -token")
	}
	c := &agentuse.Client{BaseURL: strings.TrimRight(*srvURL, "/"), Token: *token}
	me, err := c.Me(context.Background(), "")
	if err != nil {
		return err
	}
	if me.Role != agentuse.RoleAdmin {
		return fmt.Errorf("%s 不是管理员，管理命令需要管理员令牌", me.Name)
	}
	if err := saveClientConfig(*path, clientConfig{Server: c.BaseURL, Token: c.Token}); err != nil {
		return err
	}
	fmt.Printf("已登录 %s(管理员 %s),配置已写入 %s\n", c.BaseURL, me.Name, *path)
	return nil
}

func runUser(args []string) error {
	if len(args) == 0 {
		return command.UsageErr("用法: agentuse-server user create|list|token")
	}
	switch args[0] {
	case "create":
		return runUserCreate(args[1:])
	case "list":
		return runUserList(args[1:])
	case "token":
		return runUserToken(args[1:])
	}
	return command.UsageErr("未知命令 user %s", args[0])
}

// clientFlags 给管理命令加上 -server / -token / -config。
func clientFlags(name string) (*clientFlagSet, *flagsHolder) {
	fs := command.NewFlags(name)
	h := &flagsHolder{
		server: fs.String("server", "", "服务地址(默认 AGENTUSE_SERVER 或 login 存的配置)"),
		token:  fs.String("token", "", "用户令牌；用户管理需要管理员(默认 AGENTUSE_TOKEN 或 login 配置)"),
		config: fs.String("config", defaultClientConfig(), "login 存的配置文件"),
	}
	return &clientFlagSet{fs}, h
}

func runUserCreate(args []string) error {
	// 用户名放在最前面，后面才是参数:user create alice -id u_1
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return command.UsageErr("用法: agentuse-server user create 用户名 [-id 用户id] [-role user|admin]")
	}
	name := args[0]
	cf, h := clientFlags("agentuse-server user create")
	id := cf.String("id", "", "用户 id,填接入方自己的 user id;不填生成 UUID")
	role := cf.String("role", agentuse.RoleUser, "角色: user / admin")
	if err := command.Parse(cf.FlagSet, args[1:]); err != nil {
		return err
	}
	c, err := h.client()
	if err != nil {
		return err
	}
	res, err := c.CreateUser(context.Background(), agentuse.CreateUserRequest{ID: *id, Name: name, Role: *role})
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
	if err := command.Parse(cf.FlagSet, args); err != nil {
		return err
	}
	c, err := h.client()
	if err != nil {
		return err
	}
	users, err := c.ListUsers(context.Background())
	if err != nil {
		return err
	}
	if *asJSON {
		return command.PrintJSON(os.Stdout, users)
	}
	rows := [][]string{{"id", "用户名", "角色", "创建时间"}}
	for _, u := range users {
		rows = append(rows, []string{u.ID, u.Name, u.Role, time.Unix(u.CreatedAt, 0).Format("2006-01-02 15:04")})
	}
	command.PrintTable(os.Stdout, rows)
	return nil
}

func runUserToken(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return command.UsageErr("用法: agentuse-server user token 用户id")
	}
	id := args[0]
	cf, h := clientFlags("agentuse-server user token")
	if err := command.Parse(cf.FlagSet, args[1:]); err != nil {
		return err
	}
	c, err := h.client()
	if err != nil {
		return err
	}
	res, err := c.ResetUserToken(context.Background(), id)
	if err != nil {
		return err
	}
	fmt.Printf("%s(id %s)的新登录令牌: %s(旧令牌已失效)\n登录链接: %s\n", res.User.Name, res.User.ID, res.Token, res.LoginURL)
	return nil
}
