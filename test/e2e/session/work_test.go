package session

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

// 工作场景(docs/v2/designs/work.md):用户只跟协调者说话(不能自己开线程) → 协调者(laptop)用 agentuse work new
// 在另一台机器(desktop)的 agent 上开线程 →
// 线程带着工作的说明跑完 → 结果作为一条消息回到协调者，协调者再回一轮。
func TestWork(t *testing.T) {
	e2eutil.Skip(t)
	server := e2eutil.Binary(t, "agentuse-server")
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	home := t.TempDir()
	ctx := context.Background()

	srv := e2eutil.StartServer(t, server, home)
	c := agentuse.New(srv.URL, srv.AdminToken)
	const alice = "alice"
	dv, err := c.CreateDaemon(ctx, alice, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, "daemon.json")
	writeJSON(t, cfg, map[string]any{"harnesses": map[string]any{
		"claude": map[string]any{"command": fake},
		"codex":  map[string]any{"disabled": true},
	}})
	agentuseCmd := func(args ...string) *exec.Cmd {
		cmd := exec.Command(cli, append(args, "-config", cfg)...)
		cmd.Env = e2eutil.Env(home)
		return cmd
	}
	e2eutil.MustRun(t, agentuseCmd("register", "-server", srv.URL, "-token", dv.SetupToken))
	e2eutil.Start(t, agentuseCmd("daemon"))
	waitOnline(t, c, alice, dv.ID, 1)
	if _, err := c.SetDaemonHarness(ctx, alice, dv.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateAgent(ctx, alice, agentuse.CreateAgentRequest{ID: "coder", Name: "coder", DaemonID: dv.ID, Harness: "claude"}); err != nil {
		t.Fatal(err)
	}

	// 另一台机器(desktop):协调者跑在 laptop 上，线程开到 desktop 的 agent 上
	home2 := t.TempDir()
	dv2, err := c.CreateDaemon(ctx, alice, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := filepath.Join(home2, "daemon.json")
	writeJSON(t, cfg2, map[string]any{"harnesses": map[string]any{
		"claude": map[string]any{"command": fake},
		"codex":  map[string]any{"disabled": true},
	}})
	desktopCmd := func(args ...string) *exec.Cmd {
		cmd := exec.Command(cli, append(args, "-config", cfg2)...)
		cmd.Env = e2eutil.Env(home2)
		return cmd
	}
	e2eutil.MustRun(t, desktopCmd("register", "-server", srv.URL, "-token", dv2.SetupToken))
	e2eutil.Start(t, desktopCmd("daemon"))
	e2eutil.WaitFor(t, 20*time.Second, "desktop 上线", func() bool {
		ds, err := c.ListDaemons(ctx, alice)
		for _, d := range ds {
			if err == nil && d.ID == dv2.ID && d.Status == "online" && len(d.Harnesses) == 1 {
				return true
			}
		}
		return false
	})
	if _, err := c.SetDaemonHarness(ctx, alice, dv2.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateAgent(ctx, alice, agentuse.CreateAgentRequest{ID: "tester", Name: "tester", DaemonID: dv2.ID, Harness: "claude"}); err != nil {
		t.Fatal(err)
	}

	w, err := c.CreateWork(ctx, alice, agentuse.CreateWorkRequest{Name: "重构", AgentID: "coder", Instructions: "先读 README\n其余随意"})
	if err != nil || w.Coordinator.WorkRole != "coordinator" || w.Coordinator.Status != "idle" {
		t.Fatalf("建工作: %v %+v", err, w)
	}
	coord := w.Coordinator.ID
	// 线程只能由协调者开：用户自己带 work_id 建会话被拒
	body, _ := json.Marshal(map[string]string{"agent_id": "coder", "prompt": "x", "work_id": w.Work.ID})
	req, _ := http.NewRequest("POST", srv.URL+"/api/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+srv.AdminToken)
	req.Header.Set(agentuse.UserHeader, alice)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("用户手动开线程应该 403: %d", resp.StatusCode)
	}
	if _, err := c.SendMessage(ctx, alice, coord, agentuse.MessageRequest{Prompt: "spawn tester 改代码"}); err != nil {
		t.Fatal(err)
	}
	// 协调者开线程 → 线程跑完 → 结果回到协调者，协调者又回了一轮:一共 4 条消息
	e2eutil.WaitFor(t, 20*time.Second, "协调者收到线程结果并回复", func() bool {
		d, err := c.GetSession(ctx, alice, coord)
		return err == nil && d.Session.Status == "idle" && len(d.Messages) == 4
	})
	d, _ := c.GetSession(ctx, alice, coord)
	// 协调者：说明文件最前面是派活的规矩，PATH 里找得到 agentuse,工作令牌能用
	var first result
	json.Unmarshal(d.Messages[1].Content, &first)
	if !first.OK || !strings.Contains(first.Output, "claude.md=# 你是这个工作的协调者：只派活，不干活") || !strings.Contains(first.Output, "spawned=已开线程 ss_") {
		t.Fatalf("协调者: %+v", first)
	}

	threads, err := c.ListWorkSessions(ctx, alice, w.Work.ID)
	if err != nil || len(threads) != 1 || threads[0].WorkRole != "thread" || threads[0].Title != "改代码" || threads[0].AgentID != "tester" || threads[0].DaemonID != dv2.ID {
		t.Fatalf("线程应该开在 desktop 的 tester 上: %v %+v", err, threads)
	}
	// 线程真的在 desktop 上跑：会话目录在 desktop 的 home 里，不在 laptop 的
	if _, err := os.Stat(filepath.Join(home2, ".agentuse", "work", threads[0].ID)); err != nil {
		t.Fatalf("desktop 上没有线程的目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agentuse", "work", threads[0].ID)); err == nil {
		t.Fatal("laptop 上不该有线程的目录")
	}
	th := wait(t, c, alice, threads[0].ID)
	// 线程也带着说明，但拿不到工作令牌
	if r := lastResult(t, th); !r.OK || !strings.Contains(r.Output, "claude: 改代码") || !strings.Contains(r.Output, "claude.md=先读 README") {
		t.Fatalf("线程结果: %+v", r)
	}

	// 线程的结果作为一条消息回到协调者
	var note struct {
		Prompt      string `json:"prompt"`
		FromSession string `json:"from_session"`
	}
	json.Unmarshal(d.Messages[2].Content, &note)
	if note.FromSession != threads[0].ID || !strings.Contains(note.Prompt, "「改代码」回复了") || !strings.Contains(note.Prompt, "claude: 改代码") {
		t.Fatalf("协调者收到的消息: %+v", note)
	}

	// 工作里的会话不在普通列表里;标记完成;删除工作连会话一起删
	if l, _ := c.ListSessions(ctx, alice); len(l) != 0 {
		t.Fatalf("普通会话列表: %+v", l)
	}
	if s, err := c.ResolveSession(ctx, alice, threads[0].ID); err != nil || s.Status != "resolved" {
		t.Fatalf("标记完成: %v %+v", err, s)
	}
	if err := c.DeleteWork(ctx, alice, w.Work.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetSession(ctx, alice, threads[0].ID); !agentuse.IsStatus(err, 404) {
		t.Fatalf("线程应该一起删掉: %v", err)
	}
}
