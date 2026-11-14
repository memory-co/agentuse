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

func runDaemonForMulti(t *testing.T, c *agentuse.Client, user, home, cli, fake string, native bool) *agentuse.Device {
	t.Helper()
	ctx := context.Background()
	d, err := c.CreateDevice(ctx, user, filepath.Base(home))
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
		ds, e := c.ListDevices(ctx, user)
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
		if _, err = c.SetDeviceHarness(ctx, user, d.ID, name, true); err != nil {
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
	for _, a := range []agentuse.CreateAgentRequest{{ID: "coder", Name: "Coder", DeviceID: dA.ID, Harness: "fake"}, {ID: "reviewer", Name: "Reviewer", DeviceID: dB.ID, Harness: "fake"}} {
		if _, err := c.CreateAgent(ctx, user, a); err != nil {
			t.Fatal(err)
		}
	}
	created, err := c.CreateConversation(ctx, user, agentuse.CreateConversationRequest{Title: "Shared conversation"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Conversation.ID
	sharedFile, err := c.UploadAttachment(ctx, user, id, "input.txt", strings.NewReader("shared input"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "shared note"}); err != nil {
		t.Fatal(err)
	}
	first, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "multi:hold", ToAgentIDs: []string{"coder"}, AttachmentIDs: []string{sharedFile.ID}})
	if err != nil || len(first.Runs) != 0 || len(first.Deliveries) != 1 {
		t.Fatal(first, err)
	}
	var firstRun string
	e2eutil.WaitFor(t, 10*time.Second, "client accepted coder input", func() bool {
		detail, e := c.GetConversation(ctx, user, id)
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
	second, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "multi:review", ToAgentIDs: []string{"coder", "reviewer"}})
	if err != nil || len(second.Runs) != 0 || len(second.Deliveries) != 2 {
		t.Fatal(second, err)
	}
	e2eutil.WaitFor(t, 10*time.Second, "reviewer runs while coder is held", func() bool {
		detail, e := c.GetConversation(ctx, user, id)
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
	if err = os.WriteFile(filepath.Join(homeA, ".agentuse", "workspace", ep, id, "workspace", "release"), []byte("go"), 0600); err != nil {
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
	again, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "multi:review", ToAgentIDs: []string{"reviewer"}})
	if err != nil || again.Message.ID == second.Message.ID {
		t.Fatal(again, err)
	}
	if _, err = c.WaitMessageRuns(ctx, user, id, again.Message.ID, 30*time.Millisecond); err != nil {
		t.Fatal(err)
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
const created=await c.createConversation('alice',{prompt:'FYI',to_user_ids:['alice']});const {conversation}=created;
assert.deepEqual(created.message.to_agent_ids,[]);assert.deepEqual(created.message.to_user_ids,['alice']);assert.equal(conversation.status,'idle');assert.equal(created.deliveries.length,0);
assert.equal(conversation.mode,'single');
const coordination=await c.createConversation('alice',{mode:'collaborative',title:'SDK collaboration'});
assert.equal(coordination.conversation.mode,'collaborative');assert.deepEqual(await c.listChildren('alice',coordination.conversation.id),[]);
await c.deleteConversationTree('alice',coordination.conversation.id);
const req={prompt:'multi:sdk',to_agent_ids:['coder','reviewer'],to_user_ids:['alice']};const first=await c.sendMessage('alice',conversation.id,req);
assert.deepEqual(first.message.to_agent_ids,req.to_agent_ids);assert.deepEqual(first.message.to_user_ids,req.to_user_ids);
const d=await c.waitMessageRuns('alice',conversation.id,first.message.id,{interval:30,timeout:10000});assert.equal(d.runs.length,2);assert.ok(d.runs.every(r=>r.status==='completed'));
assert.ok(d.messages.filter(m=>m.author_role==='agent').every(m=>m.to_agent_ids.length===0 && m.to_user_ids.join(',')==='alice'));
assert.equal((await c.getRun('alice',conversation.id,d.runs[0].id)).run.id,d.runs[0].id);
`, filepath.Join(root, "sdk/nodejs/index.js")), nil},
		{"python", "python3", ".py", `import sys
from agentuse import Client
c=Client(sys.argv[1],sys.argv[2]);created=c.create_conversation('alice',prompt='FYI',to_user_ids=['alice']);s=created['conversation']
assert s['status']=='idle' and created['deliveries']==[] and created['message']['to_agent_ids']==[] and created['message']['to_user_ids']==['alice']
assert s['mode']=='single'
coordination=c.create_conversation('alice',mode='collaborative',title='SDK collaboration')
assert coordination['conversation']['mode']=='collaborative' and c.list_children('alice',coordination['conversation']['id'])==[]
c.delete_conversation_tree('alice',coordination['conversation']['id'])
r=c.send_message('alice',s['id'],prompt='multi:sdk',to_agent_ids=['coder','reviewer'],to_user_ids=['alice'])
assert r['message']['to_agent_ids']==['coder','reviewer'] and r['message']['to_user_ids']==['alice']
d=c.wait_message_runs('alice',s['id'],r['message']['id'],interval=.03,timeout=10)
assert len(d['runs'])==2 and all(x['status']=='completed' for x in d['runs'])
assert all(m['to_agent_ids']==[] and m['to_user_ids']==['alice'] for m in d['messages'] if m['author_role']=='agent')
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

func TestMultiNativeSteerQuestionAndNestedConversation(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	c := agentuse.New(srv.URL, srv.AdminToken)
	user := "alice"
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	d := runDaemonForMulti(t, c, user, t.TempDir(), cli, fake, true)
	workerDevice := runDaemonForMulti(t, c, user, t.TempDir(), cli, fake, false)
	for _, a := range []agentuse.CreateAgentRequest{{ID: "native", Name: "Native", DeviceID: d.ID, Harness: "claude"}, {ID: "worker", Name: "Worker", DeviceID: workerDevice.ID, Harness: "fake"}} {
		if _, err := c.CreateAgent(ctx, user, a); err != nil {
			t.Fatal(err)
		}
	}
	created, err := c.CreateConversation(ctx, user, agentuse.CreateConversationRequest{Prompt: "wait-steer", ToAgentIDs: []string{"native"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Conversation.ID
	e2eutil.WaitFor(t, 10*time.Second, "native started", func() bool { v, e := c.GetConversation(ctx, user, id); return e == nil && len(v.Events) > 1 })
	extra, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "补一个测试", ToAgentIDs: []string{"native"}})
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
	ask, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "ask", ToAgentIDs: []string{"native"}})
	if err != nil {
		t.Fatal(err)
	}
	var question agentuse.Question
	e2eutil.WaitFor(t, 10*time.Second, "public client request", func() bool {
		detail, e := c.GetConversation(ctx, user, id)
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
	parent, err := c.CreateConversation(ctx, user, agentuse.CreateConversationRequest{Title: "Collaborative", Mode: "collaborative", Prompt: "spawn worker nest-execute", ToAgentIDs: []string{"native"}})
	if err != nil {
		t.Fatal(err)
	}
	var childID string
	e2eutil.WaitFor(t, 20*time.Second, "nested conversation results", func() bool {
		children, e := c.ListChildren(ctx, user, parent.Conversation.ID)
		if e != nil || len(children) != 1 {
			return false
		}
		childID = children[0].ID
		detail, e := c.GetConversation(ctx, user, parent.Conversation.ID)
		if e != nil || detail.Conversation.Status != "idle" {
			return false
		}
		feedback := 0
		final := false
		for _, m := range detail.Messages {
			if m.SourceConversationID == childID {
				feedback++
				if m.Content == "Child execution complete." {
					final = true
				}
			}
		}
		return final && feedback == 2
	})
	children, err := c.ListChildren(ctx, user, parent.Conversation.ID)
	if err != nil || len(children) != 1 || children[0].ParentAgentID != "native" || children[0].Mode != "single" {
		t.Fatal(children, err)
	}
	child, err := c.GetConversation(ctx, user, childID)
	if err != nil || len(child.Runs) != 1 || child.Runs[0].AgentID != "worker" || child.Runs[0].DeviceID != workerDevice.ID || child.Runs[0].Status != "completed" {
		t.Fatal(child, err)
	}
	files, err := c.AllAttachments(ctx, user, childID)
	if err != nil || len(files) != 1 || files[0].RelativePath != "result.txt" {
		t.Fatal(files, err)
	}
	if err = c.DeleteConversationTree(ctx, user, parent.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetConversation(ctx, user, childID); !agentuse.IsStatus(err, 404) {
		t.Fatal("recursive delete retained child", err)
	}
}
