package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/server"
	"github.com/memory-co/agentuse/internal/store"
)

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c client) do(method, path string, body, out any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// 一个按约定从 stdin 读任务、往 stdout 写 JSON 行的假 harness。
const harness = `#!/bin/sh
read input
echo '{"type":"progress","text":"working"}'
echo "not json line"
esc=$(printf '%s' "$input" | sed 's/"/\\"/g')
echo "{\"type\":\"result\",\"ok\":true,\"output\":\"echo: $esc\"}"
`

func TestEndToEnd(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := server.New(st, server.Config{Logger: log.New(io.Discard, "", 0)})
	go srv.Run(ctx)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	_, aliceTok, _ := st.CreateUser(ctx, "", "alice", store.RoleUser)
	_, bobTok, _ := st.CreateUser(ctx, "", "bob", store.RoleUser)
	ca := client{t, ts.URL, aliceTok}
	cb := client{t, ts.URL, bobTok}

	// 自定义 agent
	if code := ca.do("POST", "/api/agents", map[string]string{"name": "echo-harness"}, nil); code != http.StatusCreated {
		t.Fatalf("create agent: %d", code)
	}

	// 页面上添加 daemon,拿到安装命令
	var dv struct {
		store.Daemon
		SetupCommand string `json:"setup_command"`
	}
	ca.do("POST", "/api/daemons", map[string]string{"name": "laptop"}, &dv)
	if dv.Status != store.DaemonPending || !strings.Contains(dv.SetupCommand, `TOKEN="`+dv.SetupToken+`" bash setup.sh`) ||
		!strings.Contains(dv.SetupCommand, ts.URL+"/setup.sh") {
		t.Fatalf("bad daemon view: %+v", dv)
	}

	// setup.sh 带上服务地址
	resp, err := http.Get(ts.URL + "/setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(script), `SERVER="${AGENTUSE_SERVER:-`+ts.URL+`}"`) {
		t.Fatalf("setup.sh 没有替换服务地址")
	}

	// 注册:令牌只能用一次
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "daemon.json")
	hpath := filepath.Join(dir, "harness.sh")
	os.WriteFile(hpath, []byte(harness), 0o755)
	if err := daemon.SaveConfig(cfgPath, daemon.Config{
		Workdir: filepath.Join(dir, "work"),
		Agents: map[string]daemon.AgentConfig{
			"echo-harness": {Command: hpath},
			"claude":       {Disabled: true},
			"codex":        {Disabled: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := daemon.Register(ctx, ts.URL, dv.SetupToken, cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.Register(ctx, ts.URL, dv.SetupToken, filepath.Join(dir, "x.json")); err == nil {
		t.Fatal("安装令牌应该只能用一次")
	}

	// 先建任务，此时 agent 还没开启，不会派
	var task store.Task
	ca.do("POST", "/api/tasks", map[string]string{"agent": "echo-harness", "prompt": "hi"}, &task)
	if task.Status != store.TaskPending {
		t.Fatalf("task: %+v", task)
	}

	d, err := daemon.New(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	go d.Run(ctx)

	// 等 daemon 上线、上报 agent
	var got struct {
		store.Daemon
	}
	waitFor(t, func() bool {
		ca.do("GET", "/api/daemons/"+dv.ID, nil, nil)
		var list []store.Daemon
		ca.do("GET", "/api/daemons", nil, &list)
		if len(list) == 1 && list[0].Status == store.DaemonOnline && len(list[0].Agents) == 1 {
			got.Daemon = list[0]
			return true
		}
		return false
	})
	if a := got.Agents[0]; a.Agent != "echo-harness" || !a.Available || a.Enabled {
		t.Fatalf("daemon agent: %+v", a)
	}

	// bob 看不到 alice 的 daemon,也开不了它的 agent
	var bobList []store.Daemon
	cb.do("GET", "/api/daemons", nil, &bobList)
	if len(bobList) != 0 {
		t.Fatalf("bob sees %d daemons", len(bobList))
	}
	if code := cb.do("PUT", "/api/daemons/"+dv.ID+"/agents/echo-harness", map[string]bool{"enabled": true}, nil); code != http.StatusNotFound {
		t.Fatalf("bob toggle: %d", code)
	}
	// bob 的同名 agent 任务不能派给 alice 的 daemon
	var bobTask store.Task
	cb.do("POST", "/api/tasks", map[string]string{"agent": "echo-harness", "prompt": "bob"}, &bobTask)

	// 开启 agent → 排队的任务被派出并完成
	if code := ca.do("PUT", "/api/daemons/"+dv.ID+"/agents/echo-harness", map[string]bool{"enabled": true}, nil); code != http.StatusOK {
		t.Fatalf("toggle: %d", code)
	}
	var detail struct {
		Task   store.Task        `json:"task"`
		Events []store.TaskEvent `json:"events"`
	}
	waitFor(t, func() bool {
		ca.do("GET", "/api/tasks/"+task.ID, nil, &detail)
		return detail.Task.Status == store.TaskDone
	})
	var res daemon.Result
	json.Unmarshal(detail.Task.Result, &res)
	if !res.OK || !strings.Contains(res.Output, `"prompt":"hi"`) || detail.Task.FinishedAt == nil || detail.Task.DaemonID != dv.ID {
		t.Fatalf("result: %+v %s", detail.Task, detail.Task.Result)
	}
	if len(detail.Events) != 3 || detail.Events[0].Kind != "progress" || detail.Events[2].Kind != "done" {
		t.Fatalf("events: %+v", detail.Events)
	}

	// 已连着的 daemon 也能马上接新任务
	var task2 store.Task
	ca.do("POST", "/api/tasks", map[string]string{"agent": "echo-harness", "prompt": "again"}, &task2)
	waitFor(t, func() bool {
		ca.do("GET", "/api/tasks/"+task2.ID, nil, &detail)
		return detail.Task.Status == store.TaskDone
	})

	if code := cb.do("GET", "/api/tasks/"+bobTask.ID, nil, &detail); code != http.StatusOK || detail.Task.Status != store.TaskPending {
		t.Fatalf("bob task should stay pending: %d %+v", code, detail.Task)
	}
	if code := cb.do("GET", "/api/tasks/"+task.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("bob should not see alice task: %d", code)
	}

	// 删除 daemon 后密钥失效
	if code := ca.do("DELETE", "/api/daemons/"+dv.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if _, err := st.DaemonByKey(ctx, cfg.Key); err == nil {
		t.Fatal("key should be invalid after delete")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout")
}
