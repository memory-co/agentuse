package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeIndexesComeOnlyFromAdapterAccepted(t *testing.T) {
	ctx := context.Background()
	files, _ := Open(t.TempDir())
	conversation, err := files.CreateLocalConversation(ctx, "native index test")
	if err != nil {
		t.Fatal(err)
	}
	session, cwd, err := files.AcquireSession(ctx, "local", conversation.ID, "agent", "codex", "v")
	if err != nil {
		t.Fatal(err)
	}
	first := "550e8400-e29b-41d4-a716-446655440000"
	second := "550e8400-e29b-41d4-a716-446655440001"
	source := filepath.Join(filepath.Dir(cwd), "sessions", session.ID, "events.jsonl")
	records := []map[string]any{
		{"type": "accepted", "run_id": first, "input_id": "one", "text": "first", "ts": "2026-11-02T10:00:00Z"},
		{"type": "accepted", "run_id": first, "input_id": "two", "text": "additional", "ts": "2026-11-02T10:00:01Z"},
		{"type": "result", "run_id": first, "status": "completed", "output": "unique result", "usage": map[string]int{"input_tokens": 42}, "cost_usd": 0, "ts": "2026-11-02T10:00:02Z"},
		{"type": "accepted", "run_id": second, "input_id": "three", "text": "second", "ts": "2026-11-02T10:00:03Z"},
		{"type": "request", "run_id": second, "request_id": "request", "state": "pending", "kind": "question", "questions": []any{map[string]string{"id": "q", "text": "question"}}, "ts": "2026-11-02T10:00:04Z"},
	}
	var body []byte
	for _, record := range records {
		body = append(body, append(raw(record), '\n')...)
	}
	if err = os.WriteFile(source, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = files.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := files.Reader().List(ctx, Filter{Endpoint: "local", Conversation: conversation.ID, Session: session.ID})
	if err != nil || len(page.Runs) != 2 {
		t.Fatal(page, err)
	}
	run, err := files.Reader().Detail(ctx, "local", conversation.ID, session.ID, first)
	if err != nil || run.Status != "completed" || run.Summary != "first" || run.Output != "unique result" || run.DurationMS != 2000 || run.Usage.InputTokens != 42 {
		t.Fatal(run, err)
	}
	queued, err := files.Reader().Detail(ctx, "local", conversation.ID, session.ID, second)
	if err != nil || queued.Status != "waiting" {
		t.Fatal(queued, err)
	}
	index := filepath.Join(filepath.Dir(source), "runs", first+".jsonl")
	data, _ := os.ReadFile(index)
	for _, forbidden := range []string{"attempt_id", "harness_run_ids", "unique result", "input_tokens"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("foreign or duplicate fact in native index", forbidden)
		}
	}
	api, _ := json.Marshal(run)
	if strings.Contains(string(api), "attempt_id") {
		t.Fatal("transport identity leaked into public run")
	}
	os.Remove(index)
	if err = files.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = files.Reader().Detail(ctx, "local", conversation.ID, session.ID, first); err != nil {
		t.Fatal("accepted run was not rebuilt", err)
	}
	if err = files.AcceptNative(ctx, session, "caller-chosen-name"); err == nil {
		t.Fatal("non-adapter run identity accepted")
	}
}
