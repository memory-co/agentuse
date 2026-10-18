package hub_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/daemon"
)

// fakeDriver 代替 claude:像 agent 一样用注入的令牌调 hub 回复,然后交回一个会话 id。
type fakeDriver struct {
	t    *testing.T
	jobs chan daemon.Job
}

func (f *fakeDriver) Run(_ context.Context, job daemon.Job) (daemon.Result, error) {
	f.jobs <- job
	var base, tok string
	for _, kv := range job.Env {
		if v, ok := strings.CutPrefix(kv, "AGENTUSE_URL="); ok {
			base = v
		}
		if v, ok := strings.CutPrefix(kv, "AGENTUSE_TOKEN="); ok {
			tok = v
		}
	}
	if code := agentPost(f.t, base, tok, "/v1/run/reply", map[string]string{"text": "done: " + strings.SplitN(job.Prompt, "\n", 2)[0]}); code != 200 {
		f.t.Errorf("fake agent reply: %d", code)
	}
	return daemon.Result{AgentSessionID: "sess_fake"}, nil
}

// 真正的 daemon 循环走 WebSocket:daemon 用设备密钥连上等着,评论来了 hub 直接推,不用等轮询。
func TestDaemonOverWebSocket(t *testing.T) {
	e := newEnv(t, envOpts{})
	repo := gitRepo(t)
	created := e.newAgent(t, "alice", "alice-claude", "")
	_, dev := e.connectDir(t, created.Connect.Code, repo)
	alice := agent{Agent: created.Agent, device: dev}
	drv := &fakeDriver{t: t, jobs: make(chan daemon.Job, 4)}
	conn := config.HubConn{URL: e.hubURL, Platform: "shop", DeviceID: alice.device.DeviceID, DeviceKey: alice.device.Key}
	home := t.TempDir()
	d, err := daemon.New(conn, config.Daemon{Transport: "ws"}, home)
	if err != nil {
		t.Fatal(err)
	}
	d.Driver = drv

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	// daemon 连上后设置页显示在线。
	waitFor(t, func() bool {
		a, err := e.sdk.Agent(context.Background(), alice.ID)
		return err == nil && a.Online
	})
	time.Sleep(50 * time.Millisecond) // 让 daemon 发出 ready
	res := e.notify(t, "c1", "alice", "member", "@alice-claude 退款金额算错了").Agents[0]

	select {
	case job := <-drv.jobs:
		if job.ResumeID != "" || !strings.Contains(job.Prompt, "退款金额算错了") {
			t.Fatalf("job: %+v", job)
		}
		// 现场是 agent 仓库里的 worktree,分支是会话分支。
		if want := filepath.Join(home, "worktrees", "shop", res.SessionID); job.Dir != want {
			t.Fatalf("job dir %s, want %s", job.Dir, want)
		}
		if b, _ := exec.Command("git", "-C", job.Dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); strings.TrimSpace(string(b)) != "agent/alice-claude/MUL-42" {
			t.Fatalf("worktree branch: %s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run was not pushed to the daemon")
	}
	select {
	case <-e.gotOne:
	case <-time.After(5 * time.Second):
		t.Fatal("platform got no reply")
	}

	// 交回后会话记住了 agent 会话 id,下一条评论续接。
	waitFor(t, func() bool {
		r, err := e.hub.Run(context.Background(), res.RunID)
		return err == nil && r.Status == "succeeded"
	})
	e.notify(t, "c2", "alice", "member", "@alice-claude 再补个测试")
	select {
	case job := <-drv.jobs:
		if job.ResumeID != "sess_fake" {
			t.Fatalf("second job should resume: %+v", job)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second run was not pushed")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// gitRepo 建一个有一次提交、在 main 分支上的仓库。
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}
