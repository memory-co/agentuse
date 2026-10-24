package cli

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	if code := ServerMain([]string{"user", "list", "-config", cfg}); code != 1 {
		t.Fatalf("没登录应该失败: %d", code)
	}
	if code := ServerMain([]string{"login", "-server", ts.URL, "-token", userTok, "-config", cfg}); code != 1 {
		t.Fatalf("普通用户令牌 login 应该失败: %d", code)
	}
	if code := ServerMain([]string{"login", "-server", ts.URL, "-token", adminTok, "-config", cfg}); code != 0 {
		t.Fatalf("login: %d", code)
	}
	if code := ServerMain([]string{"user", "create", "alice", "-id", "u_10086", "-config", cfg}); code != 0 {
		t.Fatalf("user create: %d", code)
	}
	u, err := st.GetUser(ctx, "u_10086")
	if err != nil || u.Name != "alice" || u.Role != store.RoleUser {
		t.Fatalf("created: %v %+v", err, u)
	}
	if code := ServerMain([]string{"user", "list", "-json", "-config", cfg}); code != 0 {
		t.Fatalf("user list: %d", code)
	}
	if code := ServerMain([]string{"user", "token", "u_10086", "-config", cfg}); code != 0 {
		t.Fatalf("user token: %d", code)
	}
	if code := ServerMain([]string{"user", "token", "nobody", "-config", cfg}); code != 1 {
		t.Fatalf("不存在的用户: %d", code)
	}
	// 参数优先于配置文件:用普通用户令牌调管理命令被拒
	if code := ServerMain([]string{"user", "list", "-token", userTok, "-config", cfg}); code != 1 {
		t.Fatalf("普通用户令牌应该被拒: %d", code)
	}
	if code := ServerMain([]string{"user", "create", "-id", "x"}); code != 2 {
		t.Fatalf("缺用户名应该是用法错误: %d", code)
	}
}

func quiet(t *testing.T) {
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	out, errOut := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() { os.Stdout, os.Stderr = out, errOut; devnull.Close() })
}
