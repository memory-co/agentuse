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
	cfg := filepath.Join(home, "daemon.json")
	d, err := c.CreateDaemon(ctx, user, "codex test machine")
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, cfg, map[string]any{"harnesses": map[string]any{"claude": map[string]any{"disabled": true}, "codex": map[string]any{"command": codex}}})
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
		ds, e := c.ListDaemons(ctx, user)
		if e != nil || len(ds) != 1 || ds[0].Status != "online" {
			return false
		}
		for _, cap := range ds[0].Capabilities {
			if cap == "codex-steer-v1" {
				return true
			}
		}
		return false
	})
	if _, err = c.SetDaemonHarness(ctx, user, d.ID, "codex", true); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateAgent(ctx, user, agentuse.CreateAgentRequest{ID: "codex-live", Name: "Codex live", DaemonID: d.ID, Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	prompt := "This is an integration test. Run exactly one shell command `sleep 12` and wait for it to finish. You will receive an additional user message during that command. After it finishes, follow the latest user instruction. If there is no additional instruction, respond ORIGINAL_ONLY. Do not modify files, do not use subagents, and do not read files except the session history if your instructions require it."
	created, err := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{Prompt: prompt, TargetAgentIDs: []string{"codex-live"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID
	runID := created.Runs[0].ID
	e2eutil.WaitFor(t, 90*time.Second, "real active Codex command", func() bool {
		detail, e := c.GetSession(ctx, user, id)
		if e != nil {
			return false
		}
		for _, event := range detail.Events {
			if event.Kind == "progress" {
				var v struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(event.Payload, &v)
				if strings.HasPrefix(v.Text, "🔧 ") && strings.Contains(v.Text, "sleep") {
					return true
				}
			}
		}
		if len(detail.Runs) > 0 && (detail.Runs[0].Status == "failed" || detail.Runs[0].Status == "succeeded") {
			t.Fatalf("Codex ended before steering: %+v messages=%+v", detail.Runs, detail.Messages)
		}
		return false
	})
	nonce := "STEER_PROOF_" + time.Now().UTC().Format("150405.000000000")
	sent, err := c.SendMessage(ctx, user, id, agentuse.MessageRequest{Prompt: "New requirement: reply with exactly " + nonce + " in your final answer, instead of ORIGINAL_ONLY. This updates the currently running task.", TargetAgentIDs: []string{"codex-live"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Runs) != 0 || len(sent.Deliveries) != 1 || sent.Deliveries[0].Kind != "steer" || sent.Deliveries[0].RunID != runID {
		t.Fatalf("not steered to same run: %+v", sent)
	}
	detail, err := c.WaitMessageRuns(ctx, user, id, sent.Message.ID, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].ID != runID || detail.Runs[0].Status != "succeeded" {
		t.Fatalf("steer fell back to a later run: %+v", detail.Runs)
	}
	var acknowledged bool
	var output string
	var assistantCount int
	for _, event := range detail.Events {
		if event.Kind == "steered" && event.RunID == runID {
			var ack struct {
				ID int64 `json:"steer_id"`
			}
			_ = json.Unmarshal(event.Payload, &ack)
			if ack.ID == sent.Deliveries[0].ID {
				acknowledged = true
			}
		}
	}
	for _, message := range detail.Messages {
		if message.Role == "assistant" {
			assistantCount++
			var v struct {
				Output string `json:"output"`
			}
			_ = json.Unmarshal(message.Content, &v)
			output = v.Output
		}
	}
	if !acknowledged || assistantCount != 1 || !strings.Contains(output, nonce) {
		t.Fatalf("steer not applied in original run: ack=%v assistants=%d output=%q", acknowledged, assistantCount, output)
	}
	t.Logf("real=%v session=%s run=%s: one run, matching turn/steer acknowledgment, one answer containing %s", real, id, runID, nonce)
}
