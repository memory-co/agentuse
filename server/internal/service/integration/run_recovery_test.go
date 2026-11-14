package integration

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
)

type committedNotifier struct {
	discardNotifier
	kick func()
}

func (n committedNotifier) KickAll() { n.kick() }

func TestInputWakeupRunsAfterCommit(t *testing.T) {
	s, u, _, a := runStore(t)
	ctx := context.Background()
	calls := 0
	s.SetNotifier(committedNotifier{kick: func() {
		var count int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM message_agents`).Scan(&count); err != nil || count == 0 {
			t.Fatalf("input not committed: %d %v", count, err)
		}
		calls++
	}})
	_, _, err := s.CreateConversation(ctx, u, "", "task", "hello", []string{a.ID}, nil, "", nil)
	if err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
}

func TestReconnectKeepsPreparedConversationPolicy(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	coordinator, _, err := s.CreateConversation(ctx, u, "", "coordination", "", nil, nil, "collaborative", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, u, coordinator.ID, "first", []string{a.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	original, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(original.Context, "Conversation execution policy: coordinator") {
		t.Fatal("server omitted coordinator policy")
	}
	if _, err = s.SendMessage(ctx, u, coordinator.ID, "later", []string{a.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.PendingMessages(ctx, d.ID)
	if err != nil || len(recovered) != 1 || !reflect.DeepEqual(original, recovered[0]) {
		t.Fatalf("reconnect changed prompt: %v %v", recovered, err)
	}
	var tables int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('works','work_run_tokens','run_notifications')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal(tables, err)
	}
	if err = s.RejectMessage(ctx, d.ID, original.Key(), protocol.AckError, "adapter unavailable"); err != nil {
		t.Fatal(err)
	}

}

func TestCompletedConversationDeletesExecutionRows(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, err := s.CreateConversation(ctx, u, "", "task", "hello", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	id := "550e8400-e29b-41d4-a716-446655440000"
	reportNative(t, s, d, p, "native", 1, acceptedEvent(id, "one", p.Content))
	reportNative(t, s, d, p, "native", 2, map[string]any{"type": "result", "run_id": id, "status": "completed", "input_ids": []string{"one"}, "output": "done"})
	if err = s.DeleteConversation(ctx, u, ss.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"message_agents", "runs", "run_events", "run_requests", "run_controls"} {
		var n int
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("orphan %s: %d %v", table, n, err)
		}
	}
}

func TestRejectedReplyCanRetryWithoutInventingRequestState(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, err := s.CreateConversation(ctx, u, "", "task", "hello", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	id := "550e8400-e29b-41d4-a716-446655440000"
	reportNative(t, s, d, p, "native", 1, acceptedEvent(id, "one", p.Content))
	reportNative(t, s, d, p, "native", 2, map[string]any{"type": "request", "run_id": id, "request_id": "question", "state": "pending", "kind": "question"})
	if err = s.ReplyRequest(ctx, u, ss.ID, id, "question", json.RawMessage(`{"q":"invalid"}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplyRequest(ctx, u, ss.ID, id, "question", json.RawMessage(`{"q":"duplicate"}`)); err == nil {
		t.Fatal("duplicate queued reply accepted")
	}
	controls, err := s.PendingControls(ctx, d.ID)
	if err != nil || len(controls) != 1 {
		t.Fatal(controls, err)
	}
	if err = s.ConfirmControl(ctx, d.ID, controls[0].Control.ID, protocol.AckError, "adapter rejected answer"); err != nil {
		t.Fatal(err)
	}
	questions, err := s.RunQuestions(ctx, ss.ID)
	if err != nil || len(questions) != 1 || questions[0].State != "pending" || questions[0].DeliveryError != "adapter rejected answer" {
		t.Fatal(questions, err)
	}
	if err = s.ReplyRequest(ctx, u, ss.ID, id, "question", json.RawMessage(`{"q":"valid"}`)); err != nil {
		t.Fatal(err)
	}
}
