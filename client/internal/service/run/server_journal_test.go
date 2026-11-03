package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/client/internal/fileutil"
)

func TestRunJournalAttemptsUseOneFileAndProjectExecution(t *testing.T) {
	ctx := context.Background()
	deliveries, _ := openFileServices(t.TempDir())
	session, cwd, err := deliveries.Sessions.Acquire(ctx, "ep", "c", "agent", "codex", "v")
	if err != nil {
		t.Fatal(err)
	}
	events := filepath.Join(filepath.Dir(cwd), "sessions", session.ID, "events.jsonl")
	native := `{"type":"message","run_id":"native-one","text":"UNIQUE_EXECUTION_BODY"}` + "\n" + `{"type":"result","run_id":"native-one","status":"completed","output":"UNIQUE_EXECUTION_BODY","usage":{"input_tokens":42},"cost_usd":0}` + "\n" + `{"type":"result","run_id":"native-two","status":"completed","output":"second"}` + "\n"
	if err = os.WriteFile(events, []byte(native), 0600); err != nil {
		t.Fatal(err)
	}
	writer := deliveries.ServerForEndpoint("ep")
	for _, attempt := range []string{"one", "two"} {
		run := Delivery{EndpointID: "ep", ConversationID: "c", SessionID: session.ID, RunID: "task", AttemptID: attempt}
		if err = writer.Receive(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err = writer.Complete(ctx, "task", attempt, DeliveryFinish{Status: "succeeded", HarnessRunIDs: []string{"native-" + attempt}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = deliveries.PrepareReport(ctx, "ep", "task", "one", json.RawMessage(`{"type":"event","run_id":"task","attempt_id":"one","kind":"done","payload":{"output":"UNIQUE_EXECUTION_BODY"}}`)); err != nil {
		t.Fatal(err)
	}
	if err = deliveries.Confirm(ctx, "ep", "task", "two"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(events), ".server", "task.jsonl")
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 || files[0].IsDir() {
		t.Fatal("attempts created extra directories", files)
	}
	data, _ := os.ReadFile(path)
	for _, forbidden := range []string{`"output"`, `"usage"`, `"cost_usd"`, `"events"`, "UNIQUE_EXECUTION_BODY", "must not be persisted"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("execution content duplicated in journal", forbidden)
		}
	}
	record, err := deliveries.DeliveryDetail(ctx, "ep", "c", session.ID, "task", "one")
	if err != nil || record.Output != "UNIQUE_EXECUTION_BODY" || record.Usage == nil || record.Usage.InputTokens != 42 || record.CostUSD == nil || *record.CostUSD != 0 {
		t.Fatal(record, err)
	}
	reopened, _ := openFileServices(deliveries.Sessions.Root)
	if err = reopened.ServerWriter().Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	pending := reopened.Pending("ep")
	if len(pending) != 1 || !strings.Contains(string(pending[0]), "UNIQUE_EXECUTION_BODY") {
		t.Fatal("pending attempt lost its original source", pending)
	}
	page, err := reopened.DeliveryList(ctx, Filter{Endpoint: "ep", Conversation: "c", Session: session.ID})
	if err != nil || len(page.Runs) != 2 {
		t.Fatal(page, err)
	}
	if err = reopened.Sessions.Release(session, true); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Sessions.ReclaimToLimit(ctx, 1); err == nil {
		t.Fatal("older pending attempt did not protect workspace")
	}
	// Ignore an incomplete tail on reads, and repair it before the next append.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"type":`)
	f.Close()
	if _, err = reopened.DeliveryDetail(ctx, "ep", "c", session.ID, "task", "one"); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Confirm(ctx, "ep", "task", "one"); err != nil {
		t.Fatal(err)
	}
	if pending = reopened.Pending("ep"); len(pending) != 0 {
		t.Fatal("confirmed report still pending")
	}
	final, _ := os.ReadFile(path)
	for _, line := range strings.Split(strings.TrimSpace(string(final)), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("partial tail was not repaired")
		}
	}
}

func TestLegacyRunMigrationPreservesAdapterState(t *testing.T) {
	ctx := context.Background()
	deliveries, _ := openFileServices(t.TempDir())
	session, cwd, _ := deliveries.Sessions.Acquire(ctx, "ep", "c", "agent", "codex", "v")
	root := filepath.Join(filepath.Dir(cwd), "sessions", session.ID)
	native := []byte(`{"type":"result","run_id":"native","status":"completed","output":"original"}` + "\n")
	os.WriteFile(filepath.Join(root, "events.jsonl"), native, 0600)
	private := filepath.Join(root, "events.jsonl.d", "native.json")
	fileutil.Atomic(private, []byte("untouched private state"))
	old := Delivery{EndpointID: "ep", ConversationID: "c", SessionID: session.ID, RunID: "r", AttemptID: "a", Status: "succeeded", Output: "original", HarnessRunIDs: []string{"native"}, ServerConfirmed: true}
	legacy := filepath.Join(root, "runs", "r", "a")
	if err := fileutil.WriteJSON(filepath.Join(legacy, "run.json"), old); err != nil {
		t.Fatal(err)
	}
	fileutil.Atomic(filepath.Join(legacy, "input.json"), []byte(`{"prompt":"input"}`))
	fileutil.Atomic(filepath.Join(legacy, "history.json"), []byte(`[{"id":1}]`))
	if err := deliveries.ServerWriter().Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy hierarchy retained", err)
	}
	got, err := deliveries.DeliveryDetail(ctx, "ep", "c", session.ID, "r", "a")
	if err != nil || got.Output != "original" {
		t.Fatal(got, err)
	}
	source, _ := os.ReadFile(filepath.Join(root, "events.jsonl"))
	if string(source) != string(native) {
		t.Fatal("adapter events changed")
	}
	secret, _ := os.ReadFile(private)
	if string(secret) != "untouched private state" {
		t.Fatal("private adapter state changed")
	}
	if _, err = os.Stat(filepath.Join(root, "resources", "r.a.history.json")); err != nil {
		t.Fatal(err)
	}
	if err = deliveries.ServerWriter().Reconcile(ctx); err != nil {
		t.Fatal("migration was not restart safe", err)
	}
}

func TestPreHarnessctlMigrationKeepsTheOnlyHistoricalResult(t *testing.T) {
	ctx := context.Background()
	deliveries, _ := openFileServices(t.TempDir())
	session, cwd, _ := deliveries.Sessions.Acquire(ctx, "ep", "c", "agent", "custom", "v")
	root := filepath.Join(filepath.Dir(cwd), "sessions", session.ID)
	legacy := filepath.Join(root, "runs", "task", "attempt")
	old := Delivery{EndpointID: "ep", ConversationID: "c", SessionID: session.ID, RunID: "task", AttemptID: "attempt", Status: "succeeded", Output: "old result with no adapter source", Events: []Event{{Kind: "progress", Payload: json.RawMessage(`{"text":"old progress"}`)}}, ServerConfirmed: true}
	if err := fileutil.WriteJSON(filepath.Join(legacy, "run.json"), old); err != nil {
		t.Fatal(err)
	}
	fileutil.Atomic(filepath.Join(legacy, "input.json"), []byte(`{"prompt":"full old input"}`))
	if err := deliveries.ServerWriter().Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := deliveries.DeliveryDetail(ctx, "ep", "c", session.ID, "task", "attempt")
	if err != nil || got.Output != old.Output || len(got.Events) != 1 {
		t.Fatal(got, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".server", "task.jsonl"))
	if !strings.Contains(string(data), `"type":"legacy_import"`) || !strings.Contains(string(data), "full old input") {
		t.Fatal("historical facts were lost")
	}
	if _, err = os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("old hierarchy remained", err)
	}
}

func TestRestartRecoversAcceptedBeforeClientBinding(t *testing.T) {
	ctx := context.Background()
	deliveries, _ := openFileServices(t.TempDir())
	session, cwd, _ := deliveries.Sessions.Acquire(ctx, "ep", "c", "agent", "codex", "v")
	source := filepath.Join(filepath.Dir(cwd), "sessions", session.ID, "events.jsonl")
	old := `{"type":"accepted","run_id":"older-interaction"}` + "\n"
	os.WriteFile(source, []byte(old), 0600)
	writer := deliveries.ServerForEndpoint("ep")
	if err := writer.Receive(ctx, Delivery{EndpointID: "ep", ConversationID: "c", SessionID: session.ID, RunID: "task", AttemptID: "attempt"}); err != nil {
		t.Fatal(err)
	}
	accepted := `{"type":"accepted","run_id":"new-interaction"}` + "\n" + `{"type":"result","run_id":"new-interaction","status":"completed","output":"accepted before crash"}` + "\n"
	f, _ := os.OpenFile(source, os.O_WRONLY|os.O_APPEND, 0600)
	f.WriteString(accepted)
	f.Close()
	reopened, _ := openFileServices(deliveries.Sessions.Root)
	if err := reopened.ServerWriter().Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.DeliveryDetail(ctx, "ep", "c", session.ID, "task", "attempt")
	if err != nil || got.Status != "interrupted" || len(got.HarnessRunIDs) != 1 || got.HarnessRunIDs[0] != "new-interaction" || got.Output != "accepted before crash" {
		t.Fatal(got, err)
	}
}
