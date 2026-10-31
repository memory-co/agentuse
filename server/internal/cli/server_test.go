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

	"github.com/memory-co/agentuse/internal/server"
	"github.com/memory-co/agentuse/internal/store"
)

// 管理命令只走 HTTP API:用 httptest 起服务，命令行建用户、列用户、重置令牌。
func TestServerAdminCommands(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ts := httptest.NewServer(server.New(st, server.Config{Logger: log.New(io.Discard, "", 0)}))
	defer ts.Close()
	_, adminTok, _ := st.CreateUser(ctx, "", "admin", store.RoleAdmin)
	_, userTok, _ := st.CreateUser(ctx, "", "bob", store.RoleUser)

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
	u, err := st.GetUser(ctx, "u_10086")
	if err != nil || u.Name != "alice" || u.Role != store.RoleUser {
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
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	// 空库：建 admin,打印令牌和登录链接
	if err := bootstrapAdmin(ctx, st, "http://h:1", false); err != nil {
		t.Fatal(err)
	}
	tok := regexp.MustCompile(`令牌\(只显示这一次\): (ut_\w+)`).FindStringSubmatch(logs.String())
	if tok == nil || !strings.Contains(logs.String(), "http://h:1/login?token="+tok[1]) {
		t.Fatalf("first start:\n%s", logs.String())
	}
	if u, err := st.UserByToken(ctx, tok[1]); err != nil || !u.IsAdmin() {
		t.Fatalf("admin: %v %+v", err, u)
	}

	// 再启动：什么都不做
	logs.Reset()
	if err := bootstrapAdmin(ctx, st, "http://h:1", false); err != nil || logs.Len() != 0 {
		t.Fatalf("second start: %v %q", err, logs.String())
	}
	// -reset-admin-token:旧令牌失效
	if err := bootstrapAdmin(ctx, st, "http://h:1", true); err != nil || !strings.Contains(logs.String(), "已重置") {
		t.Fatalf("reset: %v %q", err, logs.String())
	}
	if _, err := st.UserByToken(ctx, tok[1]); err == nil {
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
