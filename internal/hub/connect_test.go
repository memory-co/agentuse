package hub_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/hub"
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

// 真正的 daemon 循环走 WebSocket:daemon 先连上等着,评论来了 hub 直接推,不用等轮询。
func TestDaemonOverWebSocket(t *testing.T) {
	e := newEnv(t, hub.Options{}, "")
	drv := &fakeDriver{t: t, jobs: make(chan daemon.Job, 4)}
	cfg := config.Daemon{HubURL: e.hubURL, ID: "alice-mbp", Key: daemonKey, Transport: "ws"}
	d, err := daemon.New(cfg, t.TempDir())
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

	time.Sleep(100 * time.Millisecond) // 让 daemon 先连上、发出 ready
	res := e.notify(t, "c1", "bob", "member", "@claude 退款金额算错了")

	select {
	case job := <-drv.jobs:
		if job.ResumeID != "" || !strings.Contains(job.Prompt, "退款金额算错了") {
			t.Fatalf("job: %+v", job)
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
	e.notify(t, "c2", "bob", "member", "@claude 再补个测试")
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
