package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func reportEnvelope(delivery protocol.ConversationMessage, session string, seq int64, event json.RawMessage) protocol.ClientEvent {
	return protocol.ClientEvent{ConversationID: delivery.ConversationID, SessionID: session, MessageID: delivery.MessageID, AgentID: delivery.AgentID, AgentVersionID: delivery.AgentVersionID, Revision: "client-revision", Seq: seq, Event: event}
}

func TestV3MessagesKeepTextAuthorsAndConversationReplies(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	conversation, _, err := s.CreateConversation(ctx, u, "", "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	literal := `{"prompt":"this is literal message text"}`
	first, err := s.SendMessage(ctx, u, conversation.ID, literal, nil, nil, nil, "")
	if err != nil || first.Message.Content != literal || first.Message.AuthorRole != "user" {
		t.Fatal(first, err)
	}
	second, err := s.SendMessage(ctx, u, conversation.ID, "continue", []string{a.ID}, nil, nil, first.Message.ID)
	if err != nil || second.Message.ReplyToMessageID != first.Message.ID {
		t.Fatal(second, err)
	}
	other, _, err := s.CreateConversation(ctx, u, "", "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, u, other.ID, "cross conversation", nil, nil, nil, first.Message.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign reply accepted", err)
	}
	if messages, err := s.Messages(ctx, other.ID); err != nil || len(messages) != 0 {
		t.Fatal("failed reply left a message", messages, err)
	}
	delivery, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "client-session", 1, acceptedEvent(run, "one", "continue"))
	_, err = s.PublishAgentVersion(ctx, u, a.ID, a.CurrentVersionID, sdk.AgentConfig{Name: "New name", DeviceID: d.ID, Harness: a.Harness})
	if err != nil {
		t.Fatal(err)
	}
	text := "# Final answer\n\n| state | value |\n|---|---|\n| done | yes |"
	reportNative(t, s, d, delivery, "client-session", 2, map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"one"}, "output": text})
	messages, err := s.Messages(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	answer := messages[len(messages)-1]
	if answer.AuthorRole != "agent" || answer.AuthorAgentID != a.ID || answer.AuthorName != a.Name || answer.AuthorAgentVersionID != a.CurrentVersionID || answer.AuthorUserID != "" || answer.Content != text || answer.ReplyToMessageID != second.Message.ID {
		t.Fatal("reply lost its fixed author or text", answer)
	}
	actual, err := s.GetRun(ctx, u, conversation.ID, run)
	if err != nil || actual.SessionID != "client-session" || actual.ConversationID != conversation.ID {
		t.Fatal("native session was confused with conversation", actual, err)
	}
	late, _ := json.Marshal(map[string]any{"type": "request", "run_id": run, "request_id": "late", "state": "pending"})
	envelope := reportEnvelope(delivery, "client-session", 3, late)
	if err = s.ReportClientEvent(ctx, d.ID, envelope); err == nil {
		t.Fatal("terminal run accepted a new request")
	}
}

func TestV3ResultAndReplyRollbackTogether(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	conversation, _, err := s.CreateConversation(ctx, u, "", "", "work", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "native", 1, acceptedEvent(run, "one", "work"))
	if _, err = s.db.Exec(`CREATE TRIGGER reject_agent_reply BEFORE INSERT ON messages WHEN NEW.author_role='agent' BEGIN SELECT RAISE(FAIL,'forced reply failure'); END`); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"one"}, "output": "done"})
	envelope := reportEnvelope(delivery, "native", 2, data)
	if err = s.ReportClientEvent(ctx, d.ID, envelope); err == nil {
		t.Fatal("reply failure did not roll back result")
	}
	actual, err := s.GetRun(ctx, u, conversation.ID, run)
	if err != nil || actual.Status != "running" || actual.ResultMessageID != nil {
		t.Fatal(actual, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_agent_reply`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.ReportClientEvent(ctx, d.ID, envelope); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := s.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 2 || messages[1].Content != "done" {
		t.Fatal("result replay duplicated the reply", messages, err)
	}
}

func TestV3WorkSummaryRetriesAfterCoordinatorUnavailable(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	work, coordinator, err := s.CreateWork(ctx, u, "Work", a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	child, first, err := s.CreateConversation(ctx, u, work.ID, "Child", "do work", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "worker", 1, acceptedEvent(run, "one", "do work"))
	if err = s.SetDeviceHarnessEnabled(ctx, u, d.ID, a.Harness, false); err != nil {
		t.Fatal(err)
	}
	reportNative(t, s, d, delivery, "worker", 2, map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"one"}, "output": "child completed"})
	actual, err := s.GetRun(ctx, u, child.ID, run)
	if err != nil || actual.Status != "completed" || actual.ResultMessageID == nil {
		t.Fatal("summary failure rolled back the result", actual, err)
	}
	var notified int
	s.db.QueryRow(`SELECT count(*) FROM run_notifications WHERE message_id=?`, first.Message.ID).Scan(&notified)
	if notified != 0 {
		t.Fatal("failed summary was marked delivered")
	}
	if err = s.SetDeviceHarnessEnabled(ctx, u, d.ID, a.Harness, true); err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileWorkNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	messages, err := s.Messages(ctx, coordinator.ID)
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0].Content, "child completed") {
		t.Fatal("summary was lost or duplicated", messages, err)
	}
}

func TestV3DeviceReportsKeepUserEnablementAndCapabilities(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	report := []ReportedHarness{{Name: a.Harness, Version: "2", Revision: "package-v2", Capabilities: []string{"file.read", "file.write", "file.read"}}}
	if _, err := s.DeviceConnected(ctx, d, report); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetDevice(ctx, u, d.ID)
	if err != nil || len(current.Harnesses) != 1 {
		t.Fatal(current, err)
	}
	h := current.Harnesses[0]
	if !h.Enabled || !h.Available || h.HarnessRevision != "package-v2" || len(h.Capabilities) != 2 {
		t.Fatal("report lost user settings or metadata", h)
	}
	if err = s.SetDeviceHarnessEnabled(ctx, u, d.ID, a.Harness, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeviceConnected(ctx, d, report); err != nil {
		t.Fatal(err)
	}
	current, err = s.GetDevice(ctx, u, d.ID)
	if err != nil || current.Harnesses[0].Enabled {
		t.Fatal("heartbeat enabled a disabled harness", current, err)
	}
}

func TestV3DeviceDeletionPreservesActualRunsAndFixedVersions(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	live, _, err := s.CreateConversation(ctx, u, "", "", "live", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "native", 1, acceptedEvent(run, "one", "live"))
	sent, _, err := s.CreateConversation(ctx, u, "", "", "uncertain", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DispatchMessage(ctx, d); err != nil {
		t.Fatal(err)
	}
	pending, _, err := s.CreateConversation(ctx, u, "", "", "pending", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDevice(ctx, u, d.ID); err != nil {
		t.Fatal(err)
	}
	actual, err := s.GetRun(ctx, u, live.ID, run)
	if err != nil || actual.Status != "running" || actual.ResultMessageID != nil {
		t.Fatal("device deletion invented a native result", actual, err)
	}
	for id, want := range map[string]string{sent.ID: "unknown", pending.ID: "rejected"} {
		targets, err := s.Deliveries(ctx, id)
		if err != nil || len(targets) != 1 || targets[0].Status != want {
			t.Fatal(id, targets, err)
		}
	}
	version, err := s.GetAgentVersion(ctx, u, a.ID, a.CurrentVersionID)
	if err != nil || version.DeviceID != d.ID {
		t.Fatal("historical device binding was changed", version, err)
	}
}

func TestV3WorkCredentialsFollowTheCurrentCoordinator(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	work, coordinator, err := s.CreateWork(ctx, u, "Work", a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, u, coordinator.ID, "coordinate", []string{a.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	delivery, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "coordinator", 1, acceptedEvent(run, "one", "coordinate"))
	token, err := s.AuthorizeWorkTool(ctx, d.ID, coordinator.ID, "", a.ID, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.runWorkToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAgent(ctx, u, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.runWorkToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed coordinator retained work authority", err)
	}
	if _, err = s.WorkRunByToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed coordinator retained run access", err)
	}
	if _, err = s.AgentVersionForCredential(ctx, token, a.ID, a.CurrentVersionID); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed coordinator retained resource access", err)
	}
	if _, err = s.AttachmentConversation(ctx, AttachmentAccess{User: u, WorkID: work.ID, Token: token}, coordinator.ID, true); !errors.Is(err, ErrAttachmentUnauthorized) {
		t.Fatal("removed coordinator retained file access", err)
	}
}
