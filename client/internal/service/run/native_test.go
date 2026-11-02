package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/store"
)

func nativeFixture(t *testing.T) (*Service, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("bundled adapters require Linux")
	}
	service, home := localFixture(t)
	_, file, _, _ := runtime.Caller(0)
	source, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../../harnessctl/tests/fake_backend.py"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0700)
	for _, name := range []string{"codex", "claude"} {
		if err = os.WriteFile(filepath.Join(bin, name), source, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err = service.Harness.Freeze(nil); err != nil {
		t.Fatal(err)
	}
	return service, home
}
func waitNative(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native state did not arrive")
}
func assertNativeIdentity(t *testing.T, service *Service, run store.Run) {
	t.Helper()
	if _, err := uuid.Parse(run.RunID); err != nil {
		t.Fatal("adapter did not assign a UUID", run.RunID)
	}
	data, _ := json.Marshal(run)
	for _, foreign := range []string{"attempt_id", "harness_run_ids", "server_confirmed"} {
		if strings.Contains(string(data), foreign) {
			t.Fatal("foreign identity leaked", foreign)
		}
	}
	root := filepath.Join(service.Store.Root, run.EndpointID, run.ConversationID, "sessions", run.SessionID)
	index, err := os.ReadFile(filepath.Join(root, "runs", run.RunID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), run.RunID) {
		t.Fatal("index used a different run identity")
	}
	var found bool
	source, _ := os.ReadFile(filepath.Join(root, "events.jsonl"))
	for _, line := range strings.Split(strings.TrimSpace(string(source)), "\n") {
		var event harness.Event
		json.Unmarshal([]byte(line), &event)
		if event.Type == "accepted" && event.RunID == run.RunID {
			found = true
		}
	}
	if !found {
		t.Fatal("client invented a run without accepted")
	}
}
func TestNativeCodexSendCanKeepTheSameRun(t *testing.T) {
	service, _ := nativeFixture(t)
	ctx := context.Background()
	conversation, err := service.CreateConversation(ctx, "local", "native")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Start(ctx, "local", conversation.ID, Request{Harness: "codex", Prompt: "hold"})
	if err != nil {
		t.Fatal(err)
	}
	assertNativeIdentity(t, service, first)
	path := harness.EventsPath(localWorkspace(service, first), first.SessionID)
	waitNative(t, func() bool {
		data, _ := os.ReadFile(path)
		return strings.Contains(string(data), `"topic": "tool.started"`)
	})
	next, err := service.Control(ctx, "local", conversation.ID, first.SessionID, "send", json.RawMessage(`{"prompt":"finish"}`))
	if err != nil {
		t.Fatal(err)
	}
	if next.(store.Run).RunID != first.RunID {
		t.Fatal("client split a confirmed native steer")
	}
	result := awaitLocal(t, service, first)
	if result.Status != "completed" || result.Output != "steered" {
		t.Fatal(result)
	}
	resumed, err := service.Control(ctx, "local", conversation.ID, first.SessionID, "send", json.RawMessage(`{"prompt":"next"}`))
	if err != nil {
		t.Fatal(err)
	}
	run := resumed.(store.Run)
	if run.RunID == first.RunID || run.SessionID != first.SessionID {
		t.Fatal("idle send did not resume the exact session", run)
	}
	assertNativeIdentity(t, service, run)
	awaitLocal(t, service, run)
}
func TestNativeClaudeQueueGetsItsOwnRun(t *testing.T) {
	service, home := nativeFixture(t)
	gate := filepath.Join(home, "finish")
	t.Setenv("HARNESSCTL_TEST_RELEASE", gate)
	ctx := context.Background()
	conversation, _ := service.CreateConversation(ctx, "local", "queued")
	first, err := service.Start(ctx, "local", conversation.ID, Request{Harness: "claude", Prompt: "gated"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Control(ctx, "local", conversation.ID, first.SessionID, "send", json.RawMessage(`{"prompt":"second"}`))
	if err != nil {
		t.Fatal(err)
	}
	run := second.(store.Run)
	if run.RunID == first.RunID || run.SessionID != first.SessionID {
		t.Fatal("queued interaction lost its adapter identity", run)
	}
	assertNativeIdentity(t, service, first)
	assertNativeIdentity(t, service, run)
	os.WriteFile(gate, []byte("go"), 0600)
	awaitLocal(t, service, first)
	awaitLocal(t, service, run)
	rows, err := service.Store.Reader().List(ctx, store.Filter{Endpoint: "local", Conversation: conversation.ID, Session: first.SessionID})
	if err != nil || len(rows.Runs) != 2 {
		t.Fatal(rows, err)
	}
}
func TestNativeReplyUsesRequestIDAndInvalidReplyDoesNotCancel(t *testing.T) {
	service, _ := nativeFixture(t)
	ctx := context.Background()
	conversation, _ := service.CreateConversation(ctx, "local", "reply")
	run, err := service.Start(ctx, "local", conversation.ID, Request{Harness: "codex", Prompt: "question"})
	if err != nil {
		t.Fatal(err)
	}
	requestID := ""
	waitNative(t, func() bool {
		view, e := service.Store.Reader().Detail(ctx, "local", conversation.ID, run.SessionID, run.RunID)
		if e != nil || view.Status != "waiting" {
			return false
		}
		for _, item := range view.Events {
			var event harness.Event
			json.Unmarshal(item.Payload, &event)
			if event.Type == "request" && event.State == "pending" {
				requestID = event.RequestID
			}
		}
		return requestID != ""
	})
	state, err := service.SessionStatus(ctx, "local", conversation.ID, run.SessionID)
	if err != nil || state.(harness.State).State != "awaiting_reply" {
		t.Fatal(state, err)
	}
	invalid, _ := json.Marshal(map[string]any{"request_id": requestID, "answer": "invalid"})
	if _, err = service.Control(ctx, "local", conversation.ID, run.SessionID, "reply", invalid); err == nil {
		t.Fatal("invalid reply accepted")
	}
	valid, _ := json.Marshal(map[string]any{"request_id": requestID, "answer": "deny"})
	if _, err = service.Control(ctx, "local", conversation.ID, run.SessionID, "reply", valid); err != nil {
		t.Fatal(err)
	}
	result := awaitLocal(t, service, run)
	if result.Status != "completed" {
		t.Fatal(result)
	}
}
