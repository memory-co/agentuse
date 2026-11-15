package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/client/internal/service/session"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestNativeIndexesComeOnlyFromAdapterAccepted(t *testing.T) {
	ctx := context.Background()
	files, _ := openFileServices(t.TempDir())
	conversation, err := files.Sessions.CreateLocalConversation(ctx, "native index test")
	if err != nil {
		t.Fatal(err)
	}
	session, cwd, err := files.Sessions.Acquire(ctx, "local", conversation.ID, "agent", "codex", "v", "agent-version")
	if err != nil {
		t.Fatal(err)
	}
	first := "550e8400-e29b-41d4-a716-446655440000"
	second := "550e8400-e29b-41d4-a716-446655440001"
	source := filepath.Join(filepath.Dir(cwd), "sessions", session.ID, "events.jsonl")
	records := []map[string]any{
		{"type": "accepted", "run_id": first, "input_id": "one", "text": "first", "ts": "2026-11-02T10:00:00Z"},
		{"type": "accepted", "run_id": first, "input_id": "two", "text": "additional", "ts": "2026-11-02T10:00:01Z"},
		{"type": "result", "run_id": first, "status": "completed", "model": "native-default-model", "output": "unique result", "usage": map[string]int{"input_tokens": 42}, "cost_usd": 0, "ts": "2026-11-02T10:00:02Z"},
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
	page, err := files.List(ctx, Filter{Endpoint: "local", Conversation: conversation.ID, Session: session.ID})
	if err != nil || len(page.Runs) != 2 {
		t.Fatal(page, err)
	}
	run, err := files.Detail(ctx, "local", conversation.ID, session.ID, first)
	if err != nil || run.Status != "completed" || run.Summary != "first" || run.Output != "unique result" || run.DurationMS != 2000 || run.Usage == nil || run.Usage.InputTokens == nil || *run.Usage.InputTokens != 42 || run.Usage.CacheReadTokens != nil || run.Model == nil || *run.Model != "native-default-model" {
		t.Fatal(run, err)
	}
	queued, err := files.Detail(ctx, "local", conversation.ID, session.ID, second)
	if err != nil || queued.Status != "waiting" || queued.Model != nil || queued.Usage != nil {
		t.Fatal(queued, err)
	}
	index := filepath.Join(filepath.Dir(source), "runs", first+".jsonl")
	data, _ := os.ReadFile(index)
	for _, forbidden := range []string{"attempt_id", "harness_run_ids", "unique result", "input_tokens", "native-default-model"} {
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
	if _, err = files.Detail(ctx, "local", conversation.ID, session.ID, first); err != nil {
		t.Fatal("accepted run was not rebuilt", err)
	}
	if err = files.AcceptNative(ctx, session, "caller-chosen-name"); err == nil {
		t.Fatal("non-adapter run identity accepted")
	}
}

func TestNativeRunUsagePreservesReportedCountersAndAggregatesOnce(t *testing.T) {
	ctx := context.Background()
	files, err := openFileServices(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := files.Sessions.CreateLocalConversation(ctx, "usage")
	if err != nil {
		t.Fatal(err)
	}
	session, cwd, err := files.Sessions.Acquire(ctx, "local", conversation.ID, "agent", "codex", "v", "agent-version")
	if err != nil {
		t.Fatal(err)
	}
	count := func(n int64) *int64 { return &n }
	tests := []struct {
		id, usage string
		want      *sdk.RunUsage
	}{
		{"550e8400-e29b-41d4-a716-446655440010", `{"input_tokens":12345,"output_tokens":321,"cache_read_tokens":6789,"cache_write_tokens":0}`, &sdk.RunUsage{InputTokens: count(12345), OutputTokens: count(321), CacheReadTokens: count(6789), CacheWriteTokens: count(0)}},
		{"550e8400-e29b-41d4-a716-446655440011", `{"input_tokens":0}`, &sdk.RunUsage{InputTokens: count(0)}},
		{"550e8400-e29b-41d4-a716-446655440012", `null`, nil},
		{"550e8400-e29b-41d4-a716-446655440013", "", nil},
	}
	var body []byte
	for _, test := range tests {
		body = append(body, append(raw(map[string]any{"type": "accepted", "run_id": test.id, "input_id": test.id, "text": "work"}), '\n')...)
		event := map[string]any{"type": "result", "run_id": test.id, "status": "completed", "output": "done"}
		if test.usage != "" {
			event["usage"] = json.RawMessage(test.usage)
		}
		body = append(body, append(raw(event), '\n')...)
	}
	source := filepath.Join(filepath.Dir(cwd), "sessions", session.ID, "events.jsonl")
	if err = os.WriteFile(source, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = files.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		run, err := files.Detail(ctx, "local", conversation.ID, session.ID, test.id)
		if err != nil || !reflect.DeepEqual(run.Usage, test.want) {
			t.Fatal("per-run counters were filled or changed", run.Usage, test.want, err)
		}
	}
	stats, err := files.Stats(ctx, "harness", Filter{Endpoint: "local", Conversation: conversation.ID, Session: session.ID})
	if err != nil || len(stats) != 1 || stats[0].Runs != 4 || stats[0].UsageRuns != 2 || stats[0].Usage.InputTokens != 12345 || stats[0].Usage.OutputTokens != 321 || stats[0].Usage.CacheReadTokens != 6789 || stats[0].Usage.CacheWriteTokens != 0 {
		t.Fatal("aggregates must count only reported measurements once per run", stats, err)
	}
}

func openFileServices(root string) (*Service, error) {
	sessions, err := session.New(root)
	if err != nil {
		return nil, err
	}
	return New(Dependencies{Sessions: sessions}), nil
}
