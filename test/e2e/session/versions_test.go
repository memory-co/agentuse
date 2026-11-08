package session

import (
	"archive/zip"
	"bytes"
	"context"
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

func versionZIP(t *testing.T, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("SKILL.md")
	fmt.Fprint(f, "---\nname: review\ndescription: Version integration test\n---\n"+body)
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestAgentSkillVersionsThroughClientInit(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	c := agentuse.New(srv.URL, srv.AdminToken)
	cli := e2eutil.Binary(t, "agentuse")
	user := "version-user"
	home := t.TempDir()
	backendSource, e := os.ReadFile("../../../harnessctl/tests/fake_backend.py")
	if e != nil {
		t.Fatal(e)
	}
	backend := filepath.Join(home, "codex")
	if e = os.WriteFile(backend, backendSource, 0700); e != nil {
		t.Fatal(e)
	}
	d, e := c.CreateDaemon(ctx, user, "versions")
	if e != nil {
		t.Fatal(e)
	}
	cfg := filepath.Join(home, ".agentuse", "endpoints.yaml")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	writeAdapterConfig(t, cfg, map[string]any{"harnesses": map[string]any{"codex": map[string]any{"command": backend}, "claude": map[string]any{"disabled": true}}})
	cmd := exec.Command(cli, "register", "-server", srv.URL, "-token", d.SetupToken, "-config", cfg)
	cmd.Env = e2eutil.Env(home)
	e2eutil.MustRun(t, cmd)
	cmd = exec.Command(cli, "daemon", "-config", cfg)
	cmd.Env = e2eutil.Env(home)
	e2eutil.Start(t, cmd)
	waitOnline(t, c, user, d.ID, 1)
	if _, e = c.SetDaemonHarness(ctx, user, d.ID, "codex", true); e != nil {
		t.Fatal(e)
	}
	skill, e := c.PublishSkill(ctx, user, "", "", bytes.NewReader(versionZIP(t, "skill-one")))
	if e != nil {
		t.Fatal(e)
	}
	a, e := c.CreateAgent(ctx, user, agentuse.CreateAgentRequest{ID: "version-agent", Name: "Version Agent", DaemonID: d.ID, Harness: "codex", Model: "test-model", Instructions: "Agent one", Skills: []agentuse.SkillRef{{SkillID: skill.ID, SkillVersionID: skill.CurrentVersionID}}})
	if e != nil {
		t.Fatal(e)
	}
	created, e := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{Prompt: "hello", ToAgentIDs: []string{a.ID}})
	if e != nil {
		t.Fatal(e)
	}
	id := created.Session.ID
	first := wait(t, c, user, id)
	if len(first.Runs) != 1 || first.Runs[0].AgentVersionID != a.CurrentVersionID || first.Runs[0].Status != "completed" {
		t.Fatal(first.Runs)
	}
	firstSession := first.Runs[0].ClientSessionID
	newer, e := c.PublishSkill(ctx, user, skill.ID, skill.CurrentVersionID, bytes.NewReader(versionZIP(t, "skill-two")))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "again", ToAgentIDs: []string{a.ID}}); e != nil {
		t.Fatal(e)
	}
	second := wait(t, c, user, id)
	if len(second.Runs) != 2 {
		t.Fatal(second.Runs)
	}
	for _, r := range second.Runs {
		if r.ClientSessionID != firstSession || r.AgentVersionID != a.CurrentVersionID {
			t.Fatal("skill publication changed agent binding", r)
		}
	}
	v, e := c.PublishAgentVersion(ctx, user, a.ID, agentuse.PublishAgentVersionRequest{BaseVersionID: a.CurrentVersionID, AgentConfig: agentuse.AgentConfig{Name: a.Name, DaemonID: d.ID, Harness: "codex", Model: "test-model", Instructions: "Agent two", Skills: []agentuse.SkillRef{{SkillID: skill.ID, SkillVersionID: newer.CurrentVersionID}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "new version", ToAgentIDs: []string{a.ID}}); e != nil {
		t.Fatal(e)
	}
	third := wait(t, c, user, id)
	if len(third.Runs) != 3 {
		t.Fatal(third.Runs)
	}
	var session2 string
	for _, r := range third.Runs {
		if r.AgentVersionID == v.ID {
			session2 = r.ClientSessionID
			if r.ClientSessionID == firstSession || r.Status != "completed" {
				t.Fatal(r)
			}
		}
	}
	if session2 == "" {
		t.Fatal("new version did not execute")
	}
	last := third.Messages[len(third.Messages)-1]
	if last.AuthorAgentVersionID != v.ID {
		t.Fatal("reply lost author version", last)
	}
	for sid, want := range map[string]string{firstSession: "Agent one", session2: "Agent two"} {
		roots, e := filepath.Glob(filepath.Join(home, ".agentuse", "tmp", "*", id, "sessions", sid, "events.jsonl.d", "resources"))
		if e != nil || len(roots) != 1 {
			t.Fatal(roots, e)
		}
		data, e := os.ReadFile(filepath.Join(roots[0], "agents.md"))
		if e != nil || string(data) != want {
			t.Fatal(string(data), e)
		}
		data, e = os.ReadFile(filepath.Join(roots[0], "skills", "review", "SKILL.md"))
		suffix := "skill-one"
		if sid == session2 {
			suffix = "skill-two"
		}
		if e != nil || !strings.Contains(string(data), suffix) {
			t.Fatal(string(data), e)
		}
	}
	workspace, e := filepath.Glob(filepath.Join(home, ".agentuse", "tmp", "*", id, "workspace", "AGENTS.md"))
	if e != nil || len(workspace) != 0 {
		t.Fatal("resources polluted workspace", workspace, e)
	}
}
