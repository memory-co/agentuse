package cli

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/internal/testdb"
	"github.com/memory-co/agentuse/server/internal/database"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	server "github.com/memory-co/agentuse/server/internal/controller"
	"github.com/memory-co/agentuse/server/internal/service"
)

// 管理命令只走 HTTP API:用 httptest 起服务，命令行建用户、列用户、重置令牌。
func TestServerAdminCommands(t *testing.T) {
	st, err := service.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ts := httptest.NewServer(server.New(st, server.Config{Logger: log.New(io.Discard, "", 0)}))
	defer ts.Close()
	_, adminTok, _ := st.UserService.CreateUser(ctx, "", "admin", agentuse.RoleAdmin)
	_, userTok, _ := st.UserService.CreateUser(ctx, "", "bob", agentuse.RoleUser)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "server-cli.json")
	t.Setenv("AGENTUSE_SERVER", "")
	t.Setenv("AGENTUSE_TOKEN", "")
	quiet(t)

	if code := Main([]string{"user", "list", "-config", cfg}); code != 1 {
		t.Fatalf("没登录应该失败: %d", code)
	}
	if code := Main([]string{"login", "-server", ts.URL, "-token", userTok, "-config", cfg}); code != 1 {
		t.Fatalf("普通用户令牌 login 应该失败: %d", code)
	}
	if code := Main([]string{"login", "-server", ts.URL, "-token", adminTok, "-config", cfg}); code != 0 {
		t.Fatalf("login: %d", code)
	}
	if code := Main([]string{"user", "create", "alice", "-id", "u_10086", "-config", cfg}); code != 0 {
		t.Fatalf("user create: %d", code)
	}
	u, err := st.UserService.GetUser(ctx, "u_10086")
	if err != nil || u.Name != "alice" || u.Role != agentuse.RoleUser {
		t.Fatalf("created: %v %+v", err, u)
	}
	if code := Main([]string{"user", "list", "-json", "-config", cfg}); code != 0 {
		t.Fatalf("user list: %d", code)
	}
	if code := Main([]string{"user", "token", "u_10086", "-config", cfg}); code != 0 {
		t.Fatalf("user token: %d", code)
	}
	if code := Main([]string{"user", "token", "nobody", "-config", cfg}); code != 1 {
		t.Fatalf("不存在的用户: %d", code)
	}
	// 参数优先于配置文件:用普通用户令牌调管理命令被拒
	if code := Main([]string{"user", "list", "-token", userTok, "-config", cfg}); code != 1 {
		t.Fatalf("普通用户令牌应该被拒: %d", code)
	}
	if code := Main([]string{"user", "create", "-id", "x"}); code != 2 {
		t.Fatalf("缺用户名应该是用法错误: %d", code)
	}
}

func quiet(t *testing.T) {
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	out, errOut := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() { os.Stdout, os.Stderr = out, errOut; devnull.Close() })
}

func TestBootstrapAdmin(t *testing.T) {
	st, err := service.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	// 空库：建 admin,打印令牌和登录链接
	if err := bootstrapAdmin(ctx, st, "http://h:1", false, ""); err != nil {
		t.Fatal(err)
	}
	tok := regexp.MustCompile(`令牌\(只显示这一次\): (ut_\w+)`).FindStringSubmatch(logs.String())
	if tok == nil || !strings.Contains(logs.String(), "http://h:1/login?token="+tok[1]) {
		t.Fatalf("first start:\n%s", logs.String())
	}
	if u, err := st.UserService.UserByToken(ctx, tok[1]); err != nil || !u.IsAdmin() {
		t.Fatalf("admin: %v %+v", err, u)
	}

	// 再启动：什么都不做
	logs.Reset()
	if err := bootstrapAdmin(ctx, st, "http://h:1", false, ""); err != nil || logs.Len() != 0 {
		t.Fatalf("second start: %v %q", err, logs.String())
	}
	// -reset-admin-token:旧令牌失效
	if err := bootstrapAdmin(ctx, st, "http://h:1", true, ""); err != nil || !strings.Contains(logs.String(), "已重置") {
		t.Fatalf("reset: %v %q", err, logs.String())
	}
	if _, err := st.UserService.UserByToken(ctx, tok[1]); err == nil {
		t.Fatal("old admin token should be invalid")
	}
}

func TestDisplayAddr(t *testing.T) {
	for in, want := range map[string]string{"[::]:8080": "localhost:8080", "0.0.0.0:80": "localhost:80", "127.0.0.1:9": "127.0.0.1:9"} {
		a, _ := net.ResolveTCPAddr("tcp", in)
		if got := displayAddr(a); got != want {
			t.Errorf("displayAddr(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestChosenAdminPasswordAndStdin(t *testing.T) {
	st, err := service.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	first := "新的密码 +%\";$()&!"
	if err = bootstrapAdmin(ctx, st, "http://h:1", false, first); err != nil {
		t.Fatal(err)
	}
	admin, err := st.UserService.UserByToken(ctx, first)
	if err != nil || !admin.IsAdmin() {
		t.Fatal(admin, err)
	}
	if strings.Contains(logs.String(), first) || strings.Contains(logs.String(), "login?token=") {
		t.Fatal("chosen password leaked into startup logs")
	}
	logs.Reset()
	if err = bootstrapAdmin(ctx, st, "http://h:1", false, ""); err != nil || logs.Len() != 0 {
		t.Fatal(err, logs.String())
	}
	second := "another chosen password"
	if err = bootstrapAdmin(ctx, st, "http://h:1", false, second); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UserService.UserByToken(ctx, first); err == nil {
		t.Fatal("old password is still valid")
	}
	if got, err := st.UserService.UserByToken(ctx, second); err != nil || got.ID != admin.ID {
		t.Fatal(got, err)
	}
	if got, err := readAdminToken(strings.NewReader(second + "\n")); err != nil || got != second {
		t.Fatal(got, err)
	}
	for _, invalid := range []string{"", " ", " leading", "trailing ", "one\ntwo", "dk_reserved", strings.Repeat("a", 1025)} {
		if _, err := readAdminToken(strings.NewReader(invalid)); err == nil {
			t.Fatal("invalid chosen credential accepted")
		}
	}
}

func TestBindFailureLeavesRunningServerDeviceStateUntouched(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "server.db")
	st, err := service.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _, err := st.UserService.CreateUser(ctx, "admin", "admin", agentuse.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.DeviceService.CreateDevice(ctx, u.ID, "running device")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(ctx, st.DB, `UPDATE devices SET status='online' WHERE id=?`, d.ID); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = runStart([]string{"--addr", listener.Addr().String(), "--db", path, "--files-dir", filepath.Join(root, "files")}); err == nil {
		t.Fatal("second Server acquired occupied port")
	}
	var status string
	if err = database.Row(ctx, st.DB, `SELECT status FROM devices WHERE id=?`, d.ID).Scan(&status); err != nil || status != "online" {
		t.Fatal("failed startup changed live device state", status, err)
	}
}

func TestServerUsageErrors(t *testing.T) {
	quiet(t)
	for _, args := range [][]string{
		{"user"}, {"user", "nope"}, {"user", "token"}, {"user", "create"}, {"login"}, {"start", "-bogus"},
	} {
		if code := Main(args); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
}

func TestStartUsesSelectedBackendAndMigratesBeforeListening(t *testing.T) {
	driver, dsn := testdb.Config(t)
	if driver == "sqlite" {
		dsn = filepath.Join(t.TempDir(), "server.db")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	err = runStart([]string{"--addr", listener.Addr().String(), "--db-driver", driver, "--db", dsn, "--files-dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatal("expected listener failure after database initialization", err)
	}
	db, err := database.OpenConfig(database.Config{Driver: driver, DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !db.Connection().Migrator().HasTable("schema_migrations") {
		t.Fatal("CLI did not initialize the selected database")
	}
}
