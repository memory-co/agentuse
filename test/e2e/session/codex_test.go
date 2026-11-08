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

func TestCodexSteer(t *testing.T) {
	e2eutil.Skip(t)
	testCodexSteer(t, e2eutil.Binary(t, "fakeharness"), false)
}

// Explicit opt-in: this uses the developer's signed-in local Codex, not a mock.
func TestRealCodexSteer(t *testing.T) {
	e2eutil.Skip(t)
	if os.Getenv("AGENTUSE_REAL_CODEX") != "1" {
		t.Skip("set AGENTUSE_REAL_CODEX=1 to exercise a real signed-in Codex")
	}
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	testCodexSteer(t, bin, true)
}
func testCodexSteer(t *testing.T, codex string, real bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	server := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	cli := e2eutil.Binary(t, "agentuse")
	c := agentuse.New(server.URL, server.AdminToken)
	user := "codex-steer-test"
	home := t.TempDir()
	cfg := filepath.Join(home, "endpoints.yaml")
	d, err := c.CreateDevice(ctx, user, "codex test machine")
	if err != nil {
		t.Fatal(err)
	}
	writeAdapterConfig(t, cfg, map[string]any{"harnesses": map[string]any{"claude": map[string]any{"disabled": true}, "codex": map[string]any{"command": codex}}})
	var extra []string
	if real {
		codexHome := os.Getenv("CODEX_HOME")
		if codexHome == "" {
			originalHome, _ := os.UserHomeDir()
			codexHome = filepath.Join(originalHome, ".codex")
		}
		extra = append(extra, "CODEX_HOME="+codexHome)
	}
	command := exec.Command(cli, "register", "-server", server.URL, "-token", d.SetupToken, "-config", cfg)
	command.Env = e2eutil.Env(home, extra...)
	e2eutil.MustRun(t, command)
	command = exec.Command(cli, "daemon", "-config", cfg)
	command.Env = e2eutil.Env(home, extra...)
	e2eutil.Start(t, command)
	e2eutil.WaitFor(t, 20*time.Second, "Codex steer daemon online", func() bool {
		ds, e := c.ListDevices(ctx, user)
		if e != nil || len(ds) != 1 || ds[0].Status != "online" {
			return false
		}
		for _, cap := range ds[0].Capabilities {
			if cap == "agentuse-client/2" {
				return true
			}
		}
		return false
	})
	if _, err = c.SetDeviceHarness(ctx, user, d.ID, "codex", true); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateAgent(ctx, user, agentuse.CreateAgentRequest{ID: "codex-live", Name: "Codex live", DeviceID: d.ID, Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	prompt := "This is an integration test. Run exactly one shell command `sleep 12` and wait for it to finish. You will receive an additional user message during that command. After it finishes, follow the latest user instruction. If there is no additional instruction, respond ORIGINAL_ONLY. Do not modify files, do not use subagents, and do not read files except the session history if your instructions require it."
	created, err := c.CreateConversation(ctx, user, agentuse.CreateConversationRequest{Prompt: prompt, ToAgentIDs: []string{"codex-live"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Conversation.ID
	runID := ""
	e2eutil.WaitFor(t, 90*time.Second, "real active Codex command", func() bool {
		detail, e := c.GetConversation(ctx, user, id)
		if e != nil {
			return false
		}
		for _, event := range detail.Events {
			if event.Kind == "log" {
				var v struct {
					Topic string `json:"topic"`
					Data  struct {
						Title string `json:"title"`
					} `json:"data"`
				}
				_ = json.Unmarshal(event.Payload, &v)
				if v.Topic == "tool.started" && strings.Contains(v.Data.Title, "sleep") {
					runID = event.RunID
					return true
				}
			}
		}
		if len(detail.Runs) > 0 && (detail.Runs[0].Status == "failed" || detail.Runs[0].Status == "completed") {
			t.Fatalf("Codex ended before steering: %+v messages=%+v", detail.Runs, detail.Messages)
		}
		return false
	})
	nonce := "STEER_PROOF_" + time.Now().UTC().Format("150405.000000000")
	sent, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "New requirement: reply with exactly " + nonce + " in your final answer, instead of ORIGINAL_ONLY. This updates the currently running task.", ToAgentIDs: []string{"codex-live"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Runs) != 0 || len(sent.Deliveries) != 1 {
		t.Fatalf("not steered to same run: %+v", sent)
	}
	detail, err := c.WaitMessageRuns(ctx, user, id, sent.Message.ID, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].ID != runID || detail.Runs[0].Status != "completed" {
		t.Fatalf("steer fell back to a later run: %+v", detail.Runs)
	}
	var acknowledged bool
	var output string
	var assistantCount int
	for _, delivery := range detail.Deliveries {
		if delivery.MessageID == sent.Deliveries[0].MessageID && delivery.AgentID == sent.Deliveries[0].AgentID && delivery.RunID == runID && delivery.InputID != "" {
			acknowledged = true
		}
	}
	for _, message := range detail.Messages {
		if message.AuthorRole == "agent" {
			assistantCount++
			output = message.Content
		}
	}
	if !acknowledged || assistantCount != 1 || !strings.Contains(output, nonce) {
		t.Fatalf("steer not applied in original run: ack=%v assistants=%d output=%q", acknowledged, assistantCount, output)
	}
	files, e := filepath.Glob(filepath.Join(home, ".agentuse", "tmp", "*", id, "sessions", "*", "events.jsonl"))
	if e != nil || len(files) != 1 {
		t.Fatal("missing harnessctl session events", files, e)
	}
	data, e := os.ReadFile(files[0])
	if e != nil {
		t.Fatal(e)
	}
	accepted := 0
	results := 0
	nativeRun := ""
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev struct {
			Type, Status string
			RunID        string   `json:"run_id"`
			InputIDs     []string `json:"input_ids"`
		}
		if e = json.Unmarshal([]byte(line), &ev); e != nil {
			t.Fatal(e)
		}
		if ev.Type == "accepted" {
			accepted++
			if nativeRun == "" {
				nativeRun = ev.RunID
			}
			if ev.RunID != nativeRun {
				t.Fatal("confirmed native steer changed run", ev)
			}
		}
		if ev.Type == "result" {
			results++
			if ev.RunID != nativeRun || ev.Status != "completed" || len(ev.InputIDs) != 2 {
				t.Fatal("native interaction did not cover both inputs", ev)
			}
		}
	}
	if accepted != 2 || results != 1 {
		t.Fatal("unexpected harnessctl lifecycle", accepted, results)
	}
	t.Logf("real=%v session=%s run=%s: one run, matching turn/steer acknowledgment, one answer containing %s", real, id, runID, nonce)
}
