package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
)

func runStore(t *testing.T) (*Service, User, Daemon, Agent) {
	t.Helper()
	s := newStore(t)
	u, _, err := s.CreateUser(context.Background(), "u", "user", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	d := registered(t, s, u, "device", "custom-harness")
	a, err := s.CreateAgent(context.Background(), u.ID, "agent", "Agent", d.ID, "custom-harness", "")
	if err != nil {
		t.Fatal(err)
	}
	return s, u, d, a
}
func acceptedEvent(run, input, text string) map[string]any {
	return map[string]any{"type": "accepted", "ts": "2026-11-03T00:00:00Z", "run_id": run, "input_id": input, "text": text}
}
func reportNative(t *testing.T, s *Service, d Daemon, p protocol.ConversationMessage, session string, seq int64, event map[string]any) protocol.ClientEvent {
	t.Helper()
	raw, _ := json.Marshal(event)
	e := protocol.ClientEvent{ConversationID: p.ConversationID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, Revision: "client-revision", SessionID: session, Seq: seq, MessageID: p.MessageID, Event: raw}
	if err := s.ReportClientEvent(context.Background(), d.ID, e); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestRunsComeFromClientAcceptedAndKeepSupplementMembership(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, reply, err := s.CreateMultiSession(ctx, u, "", "", json.RawMessage(`{"prompt":"first"}`), []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Runs) != 0 || len(reply.Deliveries) != 1 {
		t.Fatal("server fabricated a run", reply)
	}
	p, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	event := reportNative(t, s, d, p, "client-session", 1, acceptedEvent(id, "input-1", p.Content))
	if err = s.ReportClientEvent(ctx, d.ID, event); err != nil {
		t.Fatal("replay failed", err)
	}
	event.Event = json.RawMessage(`{"type":"log","topic":"changed"}`)
	if err = s.ReportClientEvent(ctx, d.ID, event); err == nil {
		t.Fatal("sequence conflict accepted")
	}
	if _, err = s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"supplement"}`), []string{a.ID}, nil); err != nil {
		t.Fatal(err)
	}
	p2, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	reportNative(t, s, d, p2, "client-session", 2, acceptedEvent(id, "input-2", p2.Content))
	rows, err := s.Runs(ctx, ss.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatal(rows, err)
	}
	reportNative(t, s, d, p2, "client-session", 3, map[string]any{"type": "message", "run_id": id, "message_id": "body", "text": "client body"})
	reportNative(t, s, d, p2, "client-session", 4, map[string]any{"type": "result", "run_id": id, "status": "completed", "input_ids": []string{"input-1", "input-2"}, "output": "client result", "usage": map[string]int{"input_tokens": 17}, "cost_usd": .03})
	detail, err := s.ReadSession(ctx, u, ss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].Status != "completed" || detail.Runs[0].Output != "client result" || len(detail.Messages) != 3 {
		t.Fatalf("bad projection: %+v", detail)
	}
	answer := detail.Messages[2]
	if len(answer.ToAgentIDs) != 0 || len(answer.ToUserIDs) != 1 || answer.ToUserIDs[0] != u.ID || answer.ReplyToMessageID != reply.Message.ID || answer.AgentKey != a.ID {
		t.Fatalf("final answer must explicitly address the first sender: %+v", answer)
	}
	if len(detail.Deliveries) != 2 || len(detail.Messages[0].ToAgentIDs) != 1 || detail.Messages[0].ToAgentIDs[0] != a.ID || len(detail.Messages[0].ToUserIDs) != 0 {
		t.Fatal("result changed original recipients or scheduled another agent", detail)
	}
	for _, prompt := range detail.Deliveries {
		if prompt.RunID != id || prompt.Status != "completed" {
			t.Fatal(prompt)
		}
	}
	if _, err = s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"next"}`), []string{a.ID}, nil); err != nil {
		t.Fatal(err)
	}
	p3, _ := s.DispatchMessage(ctx, d)
	other := uuid.NewString()
	reportNative(t, s, d, p3, "client-session", 5, acceptedEvent(other, "input-3", p3.Content))
	rows, _ = s.Runs(ctx, ss.ID)
	if len(rows) != 2 {
		t.Fatal("server merged two client runs", rows)
	}
}
func TestClientRequestReplyCancelAndDisconnectAreAuthoritative(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, err := s.CreateMultiSession(ctx, u, "", "", json.RawMessage(`{"prompt":"question"}`), []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.DispatchMessage(ctx, d)
	id := uuid.NewString()
	reportNative(t, s, d, p, "client-session", 1, acceptedEvent(id, "input", p.Content))
	request := map[string]any{"type": "request", "run_id": id, "request_id": "public-request", "state": "pending", "kind": "permission", "message": "Continue?"}
	reportNative(t, s, d, p, "client-session", 2, request)
	if err = s.ReplyRequest(ctx, u, ss.ID, id, "public-request", json.RawMessage(`"deny"`)); err != nil {
		t.Fatal(err)
	}
	controls, err := s.PendingControls(ctx, d.ID)
	if err != nil || len(controls) != 1 || string(controls[0].Control.Answer) != `"deny"` {
		t.Fatal(controls, err)
	}
	if _, err = s.CancelRun(ctx, u, ss.ID, id); err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetRun(ctx, u, ss.ID, id)
	if r.Status != "waiting" {
		t.Fatal("server fabricated cancellation", r)
	}
	s.DaemonDisconnected(ctx, d.ID)

	r, _ = s.GetRun(ctx, u, ss.ID, id)
	if r.Status != "waiting" {
		t.Fatal("disconnect fabricated a native result")
	}
	reportNative(t, s, d, p, "client-session", 3, map[string]any{"type": "request", "run_id": id, "request_id": "public-request", "state": "replied", "kind": "permission", "message": "Continue?", "answer": "deny"})
	reportNative(t, s, d, p, "client-session", 4, map[string]any{"type": "result", "run_id": id, "status": "cancelled", "input_ids": []string{"input"}, "output": ""})
	r, _ = s.GetRun(ctx, u, ss.ID, id)
	if r.Status != "cancelled" {
		t.Fatal(r)
	}
}
func TestClientEventsCannotCrossOwnershipOrRebindInputs(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, _ := s.CreateMultiSession(ctx, u, "", "", json.RawMessage(`{"prompt":"first"}`), []string{a.ID}, nil)
	p, _ := s.DispatchMessage(ctx, d)
	id := uuid.NewString()
	e := reportNative(t, s, d, p, "client-session", 1, acceptedEvent(id, "input", p.Content))
	e.Seq = 2
	if err := s.ReportClientEvent(ctx, "other-device", e); err == nil {
		t.Fatal("cross-device event accepted")
	}
	e.AgentID = "other-agent"
	if err := s.ReportClientEvent(ctx, d.ID, e); err == nil {
		t.Fatal("cross-agent event accepted")
	}
	e.AgentID = a.ID
	e.Event, _ = json.Marshal(acceptedEvent(uuid.NewString(), "second", p.Content))
	if err := s.ReportClientEvent(ctx, d.ID, e); err == nil {
		t.Fatal("prompt rebound to another run")
	}
	detail, _ := s.ReadSession(ctx, u, ss.ID)
	if len(detail.Runs) != 1 || len(detail.Events) != 1 {
		t.Fatal("invalid event partially committed", detail)
	}
}
