package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestAttachments(t *testing.T) {
	e2eutil.Skip(t)
	ctx := context.Background()
	home := t.TempDir()
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), home)
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	c := agentuse.New(srv.URL, srv.AdminToken)
	const user = "alice"
	if _, err := c.CreateHarness(ctx, user, agentuse.CreateHarnessRequest{Name: "fake"}); err != nil {
		t.Fatal(err)
	}
	dv, err := c.CreateDaemon(ctx, user, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, "custom-config", "daemon.json")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	writeJSON(t, cfg, map[string]any{"harnesses": map[string]any{"fake": map[string]any{"command": fake}, "claude": map[string]any{"disabled": true}, "codex": map[string]any{"disabled": true}}})
	cmd := func(args ...string) *exec.Cmd {
		p := exec.Command(cli, append(args, "-config", cfg)...)
		p.Env = e2eutil.Env(home)
		return p
	}
	e2eutil.MustRun(t, cmd("register", "-server", srv.URL, "-token", dv.SetupToken))
	e2eutil.Start(t, cmd("daemon"))
	waitOnline(t, c, user, dv.ID, 1)
	if _, err = c.SetDaemonHarness(ctx, user, dv.ID, "fake", true); err != nil {
		t.Fatal(err)
	}
	a, err := c.CreateAgent(ctx, user, agentuse.CreateAgentRequest{Name: "coder", DaemonID: dv.ID, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{AgentID: a.ID, DeferStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Message != nil || created.Session.Status != "idle" {
		t.Fatalf("empty session %+v", created)
	}
	id := created.Session.ID
	input := "输入-data\x00\n"
	uploaded, err := c.UploadAttachment(ctx, user, id, "input.txt", strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "attachments"}); err != nil {
		t.Fatal(err)
	}
	detail := wait(t, c, user, id)
	res := lastResult(t, detail)
	if !res.OK {
		t.Fatalf("harness: %+v", res)
	}
	var output agentuse.Attachment
	if err = json.Unmarshal([]byte(res.Output), &output); err != nil {
		t.Fatal(err, res.Output)
	}
	if output.Source != "daemon" || output.DaemonID != dv.ID || output.Filename != "report.txt" {
		t.Fatalf("output metadata %+v", output)
	}
	stream, err := c.DownloadAttachment(ctx, user, id, output.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || string(body) != "result:"+input {
		t.Fatalf("download %q %v", body, err)
	}
	page, err := c.ListAttachments(ctx, user, id, 1, "")
	if err != nil || len(page.Attachments) != 1 || page.NextCursor == nil {
		t.Fatalf("list %+v %v", page, err)
	}
	page2, err := c.ListAttachments(ctx, user, id, 1, *page.NextCursor)
	if err != nil || len(page2.Attachments) != 1 || page2.NextCursor != nil {
		t.Fatalf("next %+v %v", page2, err)
	}
	// CLI with an explicit user token also works outside harnesses and without daemon credentials.
	path := filepath.Join(home, "downloaded.txt")
	command := exec.Command(cli, "attachment", "download", output.ID, "--session", id, "--server", srv.URL, "--token", srv.AdminToken, "--output", path, "--json")
	command.Env = e2eutil.Env(home)
	e2eutil.MustRun(t, command)
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, body) {
		t.Fatal("CLI download differs")
	}
	if _, err = c.GetAttachment(ctx, user, id, uploaded.ID); err != nil {
		t.Fatal(err)
	}
	testAttachmentSDKs(t, srv.URL, srv.AdminToken, id)
	if err = c.DeleteAttachment(ctx, user, id, output.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetAttachment(ctx, user, id, output.ID); !agentuse.IsStatus(err, 404) {
		t.Fatalf("delete %v", err)
	}
}
func testAttachmentSDKs(t *testing.T, server, token, session string) {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	scripts := []struct {
		name, bin, ext, code string
		env                  []string
	}{
		{"node", "node", ".mjs", fmt.Sprintf(`import { Client } from %q;
import { Readable } from 'node:stream';
import assert from 'node:assert/strict';
const [baseUrl,token,session] = process.argv.slice(2);
const c=new Client({baseUrl,token});const data=Buffer.from('node-data\x00中文');
const a=await c.uploadAttachment('alice',session,'node.txt',Readable.from([data]));
assert.equal(a.filename,'node.txt');assert.equal((await c.getAttachment('alice',session,a.id)).id,a.id);
const p=await c.listAttachments('alice',session,{limit:1});assert.equal(p.attachments.length,1);
const res=await c.downloadAttachment('alice',session,a.id);assert.deepEqual(Buffer.from(await res.arrayBuffer()),data);
await c.deleteAttachment('alice',session,a.id);
await assert.rejects(c.getAttachment('alice',session,a.id),e=>e.status===404);
`, filepath.Join(root, "sdk/nodejs/index.js")), nil},
		{"python", "python3", ".py", `import sys,io
from agentuse import Client, AgentuseError
server,token,session=sys.argv[1:]
c=Client(server,token)
data='python-data\x00中文'.encode()
a=c.upload_attachment('alice',session,'python.txt',io.BytesIO(data))
assert c.get_attachment('alice',session,a['id'])['id']==a['id']
assert len(c.list_attachments('alice',session,limit=1)['attachments'])==1
with c.download_attachment('alice',session,a['id']) as r:
    assert r.read()==data
c.delete_attachment('alice',session,a['id'])
try:
    c.get_attachment('alice',session,a['id'])
    raise AssertionError('deleted file still exists')
except AgentuseError as e:
    assert e.status==404
`, []string{"PYTHONPATH=" + filepath.Join(root, "sdk/python")}},
	}
	for _, sc := range scripts {
		t.Run(sc.name, func(t *testing.T) {
			if _, err := exec.LookPath(sc.bin); err != nil {
				t.Skip(sc.bin + " unavailable")
			}
			path := filepath.Join(t.TempDir(), "sdk"+sc.ext)
			os.WriteFile(path, []byte(sc.code), 0600)
			cmd := exec.Command(sc.bin, path, server, token, session)
			cmd.Env = append(os.Environ(), sc.env...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("SDK: %v\n%s", err, out)
			}
		})
	}
}
