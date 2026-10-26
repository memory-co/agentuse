// 普通会话场景：编好的 agentuse-server + agentuse daemon + 自研的 fakeharness,
// 走一遍 添加设备 → 注册 → 上报 harness → 建 agent → 开会话 → 回复 → 失败 → 断线重连。
// fakeharness 分别扮演自定义 harness 和 harness.yaml 描述的 claude / codex。
package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestMain(m *testing.M) { e2eutil.Main(m) }

// result 是 assistant 消息的内容(daemon 回报的结果)。
type result struct {
	OK        bool   `json:"ok"`
	Output    string `json:"output"`
	Error     string `json:"error"`
	SessionID string `json:"session_id"`
}

func lastResult(t *testing.T, d *agentuse.SessionDetail) result {
	t.Helper()
	m := d.Messages[len(d.Messages)-1]
	if m.Role != "assistant" {
		t.Fatalf("最后一条不是 assistant 消息: %+v", m)
	}
	var r result
	if err := json.Unmarshal(m.Content, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func wait(t *testing.T, c *agentuse.Client, user, id string) *agentuse.SessionDetail {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := c.WaitSession(ctx, user, id, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("等会话 %s: %v", id, err)
	}
	return d
}

func TestSession(t *testing.T) {
	e2eutil.Skip(t)
	server := e2eutil.Binary(t, "agentuse-server")
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	home := t.TempDir()
	ctx := context.Background()

	srv := e2eutil.StartServer(t, server, home)
	// 像嵌入方的后端那样用 admin 令牌 + 用户 id 调用
	c := agentuse.New(srv.URL, srv.AdminToken)
	const alice, bob = "alice", "bob"

	if _, err := c.CreateHarness(ctx, alice, agentuse.CreateHarnessRequest{Name: "fake", DisplayName: "自研 harness"}); err != nil {
		t.Fatal(err)
	}
	dv, err := c.CreateDaemon(ctx, alice, "laptop")
	if err != nil || dv.Status != "pending" || !strings.Contains(dv.SetupCommand, dv.SetupToken) {
		t.Fatalf("添加设备: %v %+v", err, dv)
	}

	// 用户电脑：daemon.json 里把三个 harness 都指向 fakeharness
	cfg := filepath.Join(home, "daemon.json")
	writeJSON(t, cfg, map[string]any{
		"harnesses": map[string]any{
			"fake":   map[string]any{"command": fake},
			"claude": map[string]any{"command": fake},
			"codex":  map[string]any{"command": fake, "args": []string{"-s", "workspace-write"}},
		},
	})
	env := e2eutil.Env(home)
	agentuseCmd := func(args ...string) *exec.Cmd {
		cmd := exec.Command(cli, append(args, "-config", cfg)...)
		cmd.Env = env
		return cmd
	}
	if out := e2eutil.MustRun(t, agentuseCmd("register", "-server", srv.URL, "-token", dv.SetupToken)); !strings.Contains(out, "注册成功") {
		t.Fatalf("register: %s", out)
	}
	if out, err := agentuseCmd("register", "-server", srv.URL, "-token", dv.SetupToken).CombinedOutput(); err == nil {
		t.Fatalf("安装令牌只能用一次: %s", out)
	}
	var local []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Found   bool   `json:"found"`
	}
	if err := json.Unmarshal([]byte(e2eutil.MustRun(t, agentuseCmd("harnesses", "-json"))), &local); err != nil {
		t.Fatal(err)
	}
	if len(local) != 3 {
		t.Fatalf("本机 harness: %+v", local)
	}
	for _, h := range local {
		// 内置的按 harness.yaml 的 detect.version 探测版本，自定义的不探测
		wantVer := "fakeharness 1.0.0"
		if h.Name == "fake" {
			wantVer = ""
		}
		if !h.Found || h.Version != wantVer {
			t.Fatalf("本机 harness: %+v", local)
		}
	}

	daemon := e2eutil.Start(t, agentuseCmd("daemon"))
	waitOnline(t, c, alice, dv.ID, 3)
	if out := e2eutil.MustRun(t, agentuseCmd("status")); !strings.Contains(out, "正常") {
		t.Fatalf("status: %s", out)
	}

	// 先建 agent、开会话，harness 还没开启 → 排队
	for _, h := range []string{"fake", "claude", "codex"} {
		if _, err := c.CreateAgent(ctx, alice, agentuse.CreateAgentRequest{ID: "alice-" + h, Name: h, DaemonID: dv.ID, Harness: h}); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := c.CreateSession(ctx, alice, agentuse.CreateSessionRequest{AgentID: "alice-fake", Prompt: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if d, _ := c.GetSession(ctx, alice, queued.Session.ID); d.Session.Status != "pending" {
		t.Fatalf("harness 没开启时不该派出: %+v", d.Session)
	}
	for _, h := range []string{"fake", "claude", "codex"} {
		if _, err := c.SetDaemonHarness(ctx, alice, dv.ID, h, true); err != nil {
			t.Fatal(err)
		}
	}
	if r := lastResult(t, wait(t, c, alice, queued.Session.ID)); !r.OK || !strings.HasPrefix(r.Output, "custom: queued") {
		t.Fatalf("排队的会话: %+v", r)
	}

	// 每种 harness:开会话 → 续接 → 失败
	for _, tc := range []struct{ harness, mode string }{
		{"fake", "custom"}, {"claude", "claude"}, {"codex", "codex"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			created, err := c.CreateSession(ctx, alice, agentuse.CreateSessionRequest{AgentID: "alice-" + tc.harness, Prompt: "hi"})
			if err != nil {
				t.Fatal(err)
			}
			id := created.Session.ID
			d := wait(t, c, alice, id)
			wantSID := tc.mode + "-" + id // fakeharness 用 模式-工作目录名 当会话 id
			r := lastResult(t, d)
			if !r.OK || r.Output != tc.mode+": hi (resume=, cwd="+id+")" || d.Session.HarnessSessionID != wantSID {
				t.Fatalf("第一条: %+v %+v", r, d.Session)
			}
			if !hasProgress(d) {
				t.Fatalf("没有进度回报: %+v", d.Events)
			}
			if tc.mode == "claude" && !hasPlan(d, `{"steps":[{"text":"读 hi","status":"completed"},{"text":"回复","status":"in_progress"}]}`) {
				t.Fatalf("没有计划回报: %+v", d.Events)
			}

			if _, err := c.SendMessage(ctx, alice, id, agentuse.MessageRequest{Prompt: "again"}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.SendMessage(ctx, alice, id, agentuse.MessageRequest{Prompt: "too soon"}); !agentuse.IsStatus(err, 409) {
				t.Fatalf("会话忙时回复应该 409: %v", err)
			}
			d = wait(t, c, alice, id)
			if r := lastResult(t, d); !r.OK || r.Output != tc.mode+": again (resume="+wantSID+", cwd="+id+")" || len(d.Messages) != 4 {
				t.Fatalf("续接: %+v", r)
			}

			if _, err := c.SendMessage(ctx, alice, id, agentuse.MessageRequest{Prompt: "fail"}); err != nil {
				t.Fatal(err)
			}
			d = wait(t, c, alice, id)
			if r := lastResult(t, d); r.OK || r.Error == "" || d.Session.HarnessSessionID != wantSID {
				t.Fatalf("失败: %+v %+v", r, d.Session)
			}
		})
	}

	// bob 用 alice 的 agent:跑在 alice 的设备上,bob 看得到;别人看不到
	at, err := c.CreateSession(ctx, bob, agentuse.CreateSessionRequest{AgentID: "alice-fake", Prompt: "from bob"})
	if err != nil || at.Session.UserID != alice || at.Session.CreatedBy != bob {
		t.Fatalf("bob 用 alice 的 agent: %v %+v", err, at)
	}
	if r := lastResult(t, wait(t, c, bob, at.Session.ID)); !r.OK || !strings.HasPrefix(r.Output, "custom: from bob") {
		t.Fatalf("bob 的会话: %+v", r)
	}
	if _, err := c.GetSession(ctx, "carol", at.Session.ID); !agentuse.IsStatus(err, 404) {
		t.Fatalf("carol 不该看到: %v", err)
	}

	// daemon 断线期间开的会话，重连后补上
	daemon.Stop()
	waitStatus(t, c, alice, dv.ID, "offline")
	offline, err := c.CreateSession(ctx, alice, agentuse.CreateSessionRequest{AgentID: "alice-claude", Prompt: "while offline"})
	if err != nil {
		t.Fatal(err)
	}
	e2eutil.Start(t, agentuseCmd("daemon"))
	if r := lastResult(t, wait(t, c, alice, offline.Session.ID)); !r.OK || !strings.HasPrefix(r.Output, "claude: while offline") {
		t.Fatalf("重连后: %+v", r)
	}

	// 删除设备 → 密钥失效,agentuse status 报失败
	if err := c.DeleteDaemon(ctx, alice, dv.ID); err != nil {
		t.Fatal(err)
	}
	if out := e2eutil.MustRun(t, agentuseCmd("status")); !strings.Contains(out, "失败") {
		t.Fatalf("删除后 status: %s", out)
	}
}

func hasProgress(d *agentuse.SessionDetail) bool {
	for _, e := range d.Events {
		if e.Kind == "progress" {
			return true
		}
	}
	return false
}

func hasPlan(d *agentuse.SessionDetail, want string) bool {
	for _, e := range d.Events {
		if e.Kind == "plan" && string(e.Payload) == want {
			return true
		}
	}
	return false
}

func waitOnline(t *testing.T, c *agentuse.Client, user, id string, harnesses int) {
	t.Helper()
	e2eutil.WaitFor(t, 20*time.Second, "设备上线并上报 harness", func() bool {
		ds, err := c.ListDaemons(context.Background(), user)
		return err == nil && len(ds) == 1 && ds[0].ID == id && ds[0].Status == "online" && len(ds[0].Harnesses) == harnesses
	})
}

func waitStatus(t *testing.T, c *agentuse.Client, user, id, status string) {
	t.Helper()
	e2eutil.WaitFor(t, 20*time.Second, "设备状态 "+status, func() bool {
		ds, err := c.ListDaemons(context.Background(), user)
		return err == nil && len(ds) == 1 && ds[0].ID == id && ds[0].Status == status
	})
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
