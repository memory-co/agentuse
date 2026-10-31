package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/harness"
	"github.com/memory-co/agentuse/internal/protocol"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("AGENTUSE_CODEX_FAKE"); mode != "" {
		fakeCodexRPC(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fakeCodexRPC(mode string) {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	send := func(v any) { _ = enc.Encode(v) }
	notify := func(method string, p any) { send(map[string]any{"method": method, "params": p}) }
	finish := func(status, text string) {
		notify("item/completed", map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"type": "agentMessage", "text": text}})
		notify("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": status, "error": map[string]string{"message": "failed test turn"}}})
	}
	for {
		var msg struct {
			ID     json.RawMessage            `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if dec.Decode(&msg) != nil {
			return
		}
		ack := func(v any) { send(map[string]any{"id": msg.ID, "result": v}) }
		switch msg.Method {
		case "initialize":
			if mode == "malformed" {
				os.Stdout.WriteString("not json\n")
				return
			}
			if mode == "init-error" {
				send(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32000, "message": "initialization failed"}})
				continue
			}
			ack(map[string]any{})
		case "thread/start":
			ack(map[string]any{"thread": map[string]string{"id": "thread-1"}})
		case "turn/start":
			ack(map[string]any{"turn": map[string]string{"id": "turn-1"}})
			notify("turn/started", map[string]any{"threadId": "thread-1", "turn": map[string]string{"id": "turn-1"}})
			notify("item/started", map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]string{"type": "commandExecution", "command": "sleep 10"}})
			if mode == "instructions" {
				b, _ := os.ReadFile("AGENTS.md")
				os.WriteFile("seen.txt", append(b, []byte("token="+os.Getenv("AGENTUSE_WORK")+"\n")...), 0600)
				finish("completed", "done")
			}
			if mode == "failed" {
				finish("failed", "")
			}
			if mode == "exit" {
				return
			}
			if mode == "cancel" {
				os.WriteFile(os.Getenv("AGENTUSE_CODEX_READY"), []byte("ready"), 0600)
			}
		case "turn/steer":
			var expected string
			_ = json.Unmarshal(msg.Params["expectedTurnId"], &expected)
			if expected != "turn-1" {
				return
			}
			if mode == "reject" {
				send(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32000, "message": "turn ended"}})
				finish("completed", "original result")
				continue
			}
			if mode == "wrong-ack" {
				ack(map[string]string{"turnId": "turn-other"})
				finish("completed", "original result")
				continue
			}
			var input []struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(msg.Params["input"], &input)
			if mode == "late-ack" {
				finish("completed", input[0].Text)
			}
			ack(map[string]string{"turnId": "turn-1"})
			notify("item/completed", map[string]any{"threadId": "thread-other", "turnId": "turn-1", "item": map[string]string{"type": "agentMessage", "text": "WRONG THREAD"}})
			if mode != "late-ack" {
				finish("completed", input[0].Text)
			}
		case "turn/interrupt":
			os.WriteFile(os.Getenv("AGENTUSE_CODEX_INTERRUPTED"), []byte("interrupted"), 0600)
			ack(map[string]any{})
			finish("interrupted", "")
		}
	}
}
func codexTestHarness(t *testing.T, mode string) localHarness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sp, _ := harness.Get("codex")
	return localHarness{Path: exe, Name: "codex", Spec: &sp, Env: []string{"AGENTUSE_CODEX_FAKE=" + mode}}
}
func TestCodexLiveSteerAcknowledgement(t *testing.T) {
	for _, mode := range []string{"success", "late-ack", "reject", "wrong-ack"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			queue := &steerQueue{}
			queue.push(protocol.Steer{ID: 7, Prompt: "nonce-from-steer"})
			var accepted int
			res := runHarness(ctx, codexTestHarness(t, mode), protocol.Dispatch{Input: json.RawMessage(`{"prompt":"original task"}`)}, t.TempDir(), &inbox{steers: queue}, func(kind string, p any) {
				if kind == "steered" {
					accepted++
				}
			})
			if !res.OK || res.SessionID != "thread-1" {
				t.Fatalf("result %+v", res)
			}
			if mode == "success" || mode == "late-ack" {
				if accepted != 1 || !strings.Contains(res.Output, "nonce-from-steer") || len(queue.take()) != 0 {
					t.Fatalf("ack %d %+v", accepted, res)
				}
			} else if accepted != 0 || len(queue.take()) != 1 {
				t.Fatalf("rejected input acknowledged/lost: %d", accepted)
			}
		})
	}
}
func TestCodexProtocolFailure(t *testing.T) {
	for _, mode := range []string{"failed", "exit", "malformed", "init-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res := runHarness(ctx, codexTestHarness(t, mode), protocol.Dispatch{Input: json.RawMessage(`{"prompt":"task"}`)}, t.TempDir(), nil, func(string, any) {})
			if res.OK || res.Error == "" {
				t.Fatalf("unexpected success %+v", res)
			}
		})
	}
}
func TestCodexArgs(t *testing.T) {
	args, err := codexArgs([]string{"--model", "custom-model", "-s", "read-only", "-c", "model_reasoning_effort=\"low\"", "--enable", "feature_x"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"app-server --listen stdio://", "model=\"custom-model\"", "sandbox_mode=\"read-only\"", "model_reasoning_effort=\"low\"", "--enable feature_x"} {
		if err != nil || !strings.Contains(joined, want) {
			t.Fatalf("%s %v %q", want, err, joined)
		}
	}
	profile, err := codexArgs([]string{"--profile", "team"})
	if err != nil || strings.Join(profile[:3], " ") != "--profile team app-server" {
		t.Fatal("profile selection", profile, err)
	}
	if _, err = codexArgs([]string{"-s"}); err == nil {
		t.Fatal("missing value")
	}
	if _, err = codexArgs([]string{"--not-supported"}); err == nil {
		t.Fatal("unknown option ignored")
	}
}

func TestCodexCancellationInterruptsTurn(t *testing.T) {
	dir := t.TempDir()
	ready := dir + "/ready"
	interrupted := dir + "/interrupted"
	la := codexTestHarness(t, "cancel")
	la.Env = append(la.Env, "AGENTUSE_CODEX_READY="+ready, "AGENTUSE_CODEX_INTERRUPTED="+interrupted)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		done <- runHarness(ctx, la, protocol.Dispatch{Input: json.RawMessage(`{"prompt":"hold"}`)}, dir, nil, func(string, any) {})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("turn did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case res := <-done:
		if res.OK {
			t.Fatal("cancelled turn succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancel hung")
	}
	if _, err := os.Stat(interrupted); err != nil {
		t.Fatal("turn/interrupt was not sent", err)
	}
}
