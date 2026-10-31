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
	cfg := filepath.Join(home, "custom", "daemon.json")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	hs := map[string]any{"fake": map[string]any{"command": fake}, "claude": map[string]any{"disabled": true}, "codex": map[string]any{"disabled": true}}
	count := 1
	if native {
		hs["claude"] = map[string]any{"command": fake}
		count = 2
	}
	writeJSON(t, cfg, map[string]any{"harnesses": hs})
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
					if cap == "session-runs-v1" {
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	c := agentuse.New(srv.URL, srv.AdminToken)
	user := "alice"
	if _, err := c.CreateHarness(ctx, user, agentuse.CreateHarnessRequest{Name: "fake"}); err != nil {
		t.Fatal(err)
	}
	homeA, homeB := t.TempDir(), t.TempDir()
	dA := runDaemonForMulti(t, c, user, homeA, cli, fake, false)
	dB := runDaemonForMulti(t, c, user, homeB, cli, fake, false)
	for _, a := range []agentuse.CreateAgentRequest{{ID: "coder", Name: "Coder", DaemonID: dA.ID, Harness: "fake"}, {ID: "reviewer", Name: "Reviewer", DaemonID: dB.ID, Harness: "fake"}} {
		if _, err := c.CreateAgent(ctx, user, a); err != nil {
			t.Fatal(err)
		}
	}
	created, err := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{Mode: "multi_agent", Title: "Shared session"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID
	if created.Message != nil {
		t.Fatal("session binding", created)
	}
	if _, err = c.UploadAttachment(ctx, user, id, "input.txt", strings.NewReader("shared input")); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "shared note"}); err != nil {
		t.Fatal(err)
	}
	first, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "multi:hold", TargetAgentIDs: []string{"coder"}})
	if err != nil || len(first.Runs) != 1 {
		t.Fatal("first", first, err)
	}
	e2eutil.WaitFor(t, 10*time.Second, "coder executing", func() bool {
		d, e := c.GetSession(ctx, user, id)
		return e == nil && len(d.Events) > 1 && d.Session.Status == "running"
	})
	req := agentuse.MessageRequest{Prompt: "multi:review", TargetAgentIDs: []string{"coder", "reviewer"}, IdempotencyKey: "turn2"}
	second, err := c.SendMessage(ctx, user, id, req)
	if err != nil || len(second.Runs) != 1 || second.Runs[0].AgentKey != "reviewer" {
		t.Fatalf("mixed send %+v %v", second, err)
	}
	repeated, err := c.SendMessage(ctx, user, id, req)
	if err != nil || repeated.Message.ID != second.Message.ID {
		t.Fatal("idempotency", repeated, err)
	}
	bResult, err := c.WaitRun(ctx, user, id, second.Runs[0].ID, 50*time.Millisecond)
	if err != nil || bResult.Run.Status != "succeeded" {
		t.Fatal("parallel B", bResult, err)
	}
	aResult, err := c.GetRun(ctx, user, id, first.Runs[0].ID)
	if err != nil || aResult.Run.Status != "running" {
		t.Fatal("A should still hold", aResult, err)
	}
	os.WriteFile(filepath.Join(homeA, ".agentuse", "work", id, "release"), []byte("go"), 0600)
	detail, err := c.WaitMessageRuns(ctx, user, id, second.Message.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 3 || len(detail.Messages) != 6 {
		t.Fatalf("fallback duplicated history: runs=%d messages=%d", len(detail.Runs), len(detail.Messages))
	}
	var lastA agentuse.Run
	for _, r := range detail.Runs {
		if r.AgentKey == "coder" && r.MessageID == second.Message.ID {
			lastA = r
		}
	}
	if lastA.ID == "" || lastA.Status != "succeeded" {
		t.Fatal("fallback missing", lastA)
	}
	for _, m := range detail.Messages {
		if m.Role != "assistant" {
			continue
		}
		var r result
		if err = json.Unmarshal(m.Content, &r); err != nil || !r.OK {
			t.Fatal("result", r, err)
		}
		var out struct {
			Resume  string             `json:"resume"`
			History []agentuse.Message `json:"history"`
			RunID   string             `json:"run_id"`
			Live    []agentuse.Message `json:"live"`
		}
		if err = json.Unmarshal([]byte(r.Output), &out); err != nil {
			t.Fatal(err)
		}
		if out.Resume != "" || out.RunID != m.RunID || len(out.History) < 2 || len(out.Live) < 2 {
			t.Fatalf("shared context %+v", out)
		}
		if m.RunID == lastA.ID {
			found := false
			for _, h := range out.History {
				if h.AgentKey == "reviewer" {
					found = true
				}
			}
			if !found {
				t.Fatal("A cannot see reviewer result")
			}
		}
	}
	// New runs pick the agent's current runtime; no per-agent resume or rebind step.
	if _, err = c.UpdateAgent(ctx, user, "coder", agentuse.UpdateAgentRequest{DaemonID: &dB.ID, Harness: ptrString("fake")}); err != nil {
		t.Fatal(err)
	}
	switched, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "attachments", TargetAgentIDs: []string{"coder"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(switched.Runs) != 1 || switched.Runs[0].DaemonID != dB.ID {
		t.Fatal("runtime switch", switched)
	}
	detail, err = c.WaitMessageRuns(ctx, user, id, switched.Message.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	r := lastResult(t, detail)
	if !r.OK {
		t.Fatal("run attachment", r)
	}
	var a agentuse.Attachment
	if err = json.Unmarshal([]byte(r.Output), &a); err != nil || a.CreatedByRunID != switched.Runs[0].ID {
		t.Fatal("attachment attribution", a, err)
	}
	testMultiSDKs(t, srv.URL, srv.AdminToken)
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
	if _, err := c.CreateHarness(ctx, user, agentuse.CreateHarnessRequest{Name: "fake"}); err != nil {
		t.Fatal(err)
	}
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
	if err != nil || len(extra.Runs) != 0 || len(extra.Deliveries) != 1 || extra.Deliveries[0].Kind != "steer" {
		t.Fatal("native steer", extra, err)
	}
	detail, err := c.WaitMessageRuns(ctx, user, id, extra.Message.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 1 || len(detail.Messages) != 3 {
		t.Fatalf("steer created extra run/message: %+v", detail)
	}
	if r := lastResult(t, detail); !r.OK || !strings.Contains(r.Output, "补一个测试") {
		t.Fatal("native steer result", r)
	}
	ask, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "ask", TargetAgentIDs: []string{"native"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.WaitRun(ctx, user, id, ask.Runs[0].ID, 50*time.Millisecond, agentuse.WaitOptions{ReturnOnWaiting: true}); err != nil {
		t.Fatal(err)
	}
	detail, err = c.GetSession(ctx, user, id)
	if err != nil {
		t.Fatal(err)
	}
	var question agentuse.Question
	for _, q := range detail.Questions {
		if q.RunID == ask.Runs[0].ID {
			question = q
		}
	}
	if question.ID == 0 {
		t.Fatal("run question missing")
	}
	if _, err = c.AnswerRunQuestion(ctx, user, id, question.RunID, question.ID, map[string]string{"用哪种语言？": "Go"}); err != nil {
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
	if err != nil || len(child.Runs) != 1 || child.Runs[0].AgentKey != "worker" || child.Runs[0].Status != "succeeded" {
		t.Fatal("child", child, err)
	}
}
