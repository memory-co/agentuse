package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"net/url"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func runDaemonForMulti(t *testing.T, c *agentuse.Client, user, home, cli, fake string, native bool) *agentuse.Daemon {
	t.Helper()
	ctx := context.Background()
	d, err := c.CreateDaemon(ctx, user, filepath.Base(home))
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, "custom", "endpoints.yaml")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	hs := map[string]any{"fake": map[string]any{"command": fake}, "claude": map[string]any{"disabled": true}, "codex": map[string]any{"disabled": true}}
	count := 1
	if native {
		hs["claude"] = map[string]any{"command": fake}
		count = 2
	}
	writeAdapterConfig(t, cfg, map[string]any{"harnesses": hs})
	cmd := exec.Command(cli, "register", "-server", c.BaseURL, "-token", d.SetupToken, "-config", cfg)
	cmd.Env = e2eutil.Env(home)
	e2eutil.MustRun(t, cmd)
	cmd = exec.Command(cli, "daemon", "-config", cfg)
	cmd.Env = e2eutil.Env(home)
	e2eutil.Start(t, cmd)
	e2eutil.WaitFor(t, 10*time.Second, "run daemon online", func() bool {
		ds, e := c.ListDaemons(ctx, user)
		if e != nil {
			return false
		}
		for _, item := range ds {
			if item.ID == d.ID && item.Status == "online" && len(item.Harnesses) == count {
				for _, cap := range item.Capabilities {
					if cap == "agentuse-client/2" {
						return true
					}
				}
			}
		}
		return false
	})
	for _, name := range []string{"fake", "claude"} {
		if name == "claude" && !native {
			continue
		}
		if _, err = c.SetDaemonHarness(ctx, user, d.ID, name, true); err != nil {
			t.Fatal(err)
		}
	}
	return d
}
func TestMultiAgentSession(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	c := agentuse.New(srv.URL, srv.AdminToken)
	user := "alice"
	homeA, homeB := t.TempDir(), t.TempDir()
	dA := runDaemonForMulti(t, c, user, homeA, cli, fake, false)
	dB := runDaemonForMulti(t, c, user, homeB, cli, fake, false)
	for _, a := range []agentuse.CreateAgentRequest{{ID: "coder", Name: "Coder", DaemonID: dA.ID, Harness: "fake"}, {ID: "reviewer", Name: "Reviewer", DaemonID: dB.ID, Harness: "fake"}} {
		if _, err := c.CreateAgent(ctx, user, a); err != nil {
			t.Fatal(err)
		}
	}
	created, err := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{Title: "Shared conversation"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID
	if _, err = c.UploadAttachment(ctx, user, id, "input.txt", strings.NewReader("shared input")); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "shared note"}); err != nil {
		t.Fatal(err)
	}
	first, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "multi:hold", TargetAgentIDs: []string{"coder"}})
	if err != nil || len(first.Runs) != 0 || len(first.Deliveries) != 1 {
		t.Fatal(first, err)
	}
	var firstRun string
	e2eutil.WaitFor(t, 10*time.Second, "client accepted coder input", func() bool {
		detail, e := c.GetSession(ctx, user, id)
		if e == nil {
			for _, r := range detail.Runs {
				if r.AgentID == "coder" && r.Status == "running" {
					firstRun = r.ID
					return true
				}
			}
		}
		return false
	})
	second, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "multi:review", TargetAgentIDs: []string{"coder", "reviewer"}})
	if err != nil || len(second.Runs) != 0 || len(second.Deliveries) != 2 {
		t.Fatal(second, err)
	}
	e2eutil.WaitFor(t, 10*time.Second, "reviewer runs while coder is held", func() bool {
		detail, e := c.GetSession(ctx, user, id)
		if e != nil {
			return false
		}
		for _, r := range detail.Runs {
			if r.AgentID == "reviewer" && r.Status == "failed" {
				t.Fatalf("reviewer failed: %+v", r)
			}
			if r.AgentID == "reviewer" && r.Status == "completed" {
				return true
			}
		}
		return false
	})
	held, err := c.GetRun(ctx, user, id, firstRun)
	if err != nil || held.Run.Status != "running" {
		t.Fatal(held, err)
	}
	u, _ := url.Parse(srv.URL)
	ep := "http---" + strings.ReplaceAll(u.Host, ":", "%3A")
	if err = os.WriteFile(filepath.Join(homeA, ".agentuse", "tmp", ep, id, "workspace", "release"), []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	detail, err := c.WaitMessageRuns(ctx, user, id, second.Message.ID, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 3 {
		t.Fatal("custom adapter assigned three native interactions; server must preserve them", detail.Runs)
	}
	for _, r := range detail.Runs {
		if r.Status != "completed" {
			t.Fatal(r)
		}
	}
	// A new message is always new input, even if its text is identical.
	again, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "multi:review", TargetAgentIDs: []string{"reviewer"}})
	if err != nil || again.Message.ID == second.Message.ID {
		t.Fatal(again, err)
	}
	if _, err = c.WaitMessageRuns(ctx, user, id, again.Message.ID, 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func ptrString(s string) *string { return &s }
func testMultiSDKs(t *testing.T, server, token string) {
	root, _ := filepath.Abs("../../..")
	scripts := []struct {
		name, bin, ext, code string
		env                  []string
	}{
		{"node", "node", ".mjs", fmt.Sprintf(`import {Client} from %q;
import assert from 'node:assert/strict';const [baseUrl,token]=process.argv.slice(2);const c=new Client({baseUrl,token});
const {session}=await c.createSession('alice',{mode:'multi_agent'});
const req={prompt:'multi:sdk',target_agent_ids:['coder','reviewer']};const first=await c.sendMessage('alice',session.id,req,{idempotencyKey:'sdk'});
assert.equal((await c.sendMessage('alice',session.id,req,{idempotencyKey:'sdk'})).message.id,first.message.id);
const d=await c.waitMessageRuns('alice',session.id,first.message.id,{interval:30,timeout:10000});assert.equal(d.runs.length,2);assert.ok(d.runs.every(r=>r.status==='succeeded'));
assert.equal((await c.getRun('alice',session.id,d.runs[0].id)).run.id,d.runs[0].id);
`, filepath.Join(root, "sdk/nodejs/index.js")), nil},
		{"python", "python3", ".py", `import sys
from agentuse import Client
c=Client(sys.argv[1],sys.argv[2]);s=c.create_session('alice',mode='multi_agent')['session']
r=c.send_message('alice',s['id'],prompt='multi:sdk',target_agent_ids=['coder','reviewer'],idempotency_key='sdk')
assert c.send_message('alice',s['id'],prompt='multi:sdk',target_agent_ids=['coder','reviewer'],idempotency_key='sdk')['message']['id']==r['message']['id']
d=c.wait_message_runs('alice',s['id'],r['message']['id'],interval=.03,timeout=10)
assert len(d['runs'])==2 and all(x['status']=='succeeded' for x in d['runs'])
assert c.get_run('alice',s['id'],d['runs'][0]['id'])['run']['id']==d['runs'][0]['id']
`, []string{"PYTHONPATH=" + filepath.Join(root, "sdk/python")}},
	}
	for _, s := range scripts {
		t.Run(s.name, func(t *testing.T) {
			if _, err := exec.LookPath(s.bin); err != nil {
				t.Skip(s.bin + " unavailable")
			}
			p := filepath.Join(t.TempDir(), "multi"+s.ext)
			os.WriteFile(p, []byte(s.code), 0600)
			cmd := exec.Command(s.bin, p, server, token)
			cmd.Env = append(os.Environ(), s.env...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("SDK %v\n%s", err, out)
			}
		})
	}
}

func TestMultiNativeSteerQuestionAndWork(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	c := agentuse.New(srv.URL, srv.AdminToken)
	user := "alice"
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	d := runDaemonForMulti(t, c, user, t.TempDir(), cli, fake, true)
	for _, a := range []agentuse.CreateAgentRequest{{ID: "native", Name: "Native", DaemonID: d.ID, Harness: "claude"}, {ID: "worker", Name: "Worker", DaemonID: d.ID, Harness: "fake"}} {
		if _, err := c.CreateAgent(ctx, user, a); err != nil {
			t.Fatal(err)
		}
	}
	created, err := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{Mode: "multi_agent", Prompt: "wait-steer", TargetAgentIDs: []string{"native"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID
	e2eutil.WaitFor(t, 10*time.Second, "native started", func() bool { v, e := c.GetSession(ctx, user, id); return e == nil && len(v.Events) > 1 })
	extra, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "补一个测试", TargetAgentIDs: []string{"native"}})
	if err != nil || len(extra.Runs) != 0 || len(extra.Deliveries) != 1 {
		t.Fatal("native steer", extra, err)
	}
	detail, err := c.WaitMessageRuns(ctx, user, id, extra.Message.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 2 || len(detail.Messages) != 4 {
		t.Fatalf("steer created extra run/message: %+v", detail)
	}
	if r := lastResult(t, detail); !r.OK || !strings.Contains(r.Output, "补一个测试") {
		t.Fatal("native steer result", r)
	}
	ask, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "ask", TargetAgentIDs: []string{"native"}})
	if err != nil {
		t.Fatal(err)
	}
	var question agentuse.Question
	e2eutil.WaitFor(t, 10*time.Second, "public client request", func() bool {
		detail, e := c.GetSession(ctx, user, id)
		if e != nil {
			return false
		}
		for _, q := range detail.Questions {
			if q.State == "pending" {
				question = q
				return true
			}
		}
		return false
	})
	var body struct {
		Questions []struct {
			ID string `json:"id"`
		}
	}
	if err = json.Unmarshal(question.Payload, &body); err != nil || len(body.Questions) != 1 {
		t.Fatal(question, err)
	}
	if err = c.ReplyRunRequest(ctx, user, id, question.RunID, question.RequestID, map[string]string{body.Questions[0].ID: "Go"}); err != nil {
		t.Fatal(err)
	}
	detail, err = c.WaitMessageRuns(ctx, user, id, ask.Message.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r := lastResult(t, detail); !r.OK || !strings.Contains(r.Output, "answer=Go") {
		t.Fatal("answer", r)
	}
	work, err := c.CreateWork(ctx, user, agentuse.CreateWorkRequest{Mode: "multi_agent", Name: "Multi work", AgentID: "native", Instructions: "Use shared session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SendMessage(ctx, user, work.Coordinator.ID, agentuse.MessageRequest{Prompt: "spawn worker multi:child", TargetAgentIDs: []string{"native"}}); err != nil {
		t.Fatal(err)
	}
	e2eutil.WaitFor(t, 15*time.Second, "work aggregation", func() bool {
		v, e := c.GetSession(ctx, user, work.Coordinator.ID)
		return e == nil && v.Session.Status == "idle" && len(v.Messages) == 4
	})
	coor, err := c.GetSession(ctx, user, work.Coordinator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(coor.Runs) != 2 {
		t.Fatal("coordinator runs", coor.Runs)
	}
	threads, err := c.ListWorkSessions(ctx, user, work.Work.ID)
	if err != nil || len(threads) != 1 || threads[0].Mode != "multi_agent" {
		t.Fatal("work thread", threads, err)
	}
	child, err := c.GetSession(ctx, user, threads[0].ID)
	if err != nil || len(child.Runs) != 1 || child.Runs[0].AgentID != "worker" || child.Runs[0].Status != "completed" {
		t.Fatal("child", child, err)
	}
}
