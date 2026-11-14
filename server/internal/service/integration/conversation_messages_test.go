package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestMessagesKeepTextAuthorsAndConversationReplies(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	conversation, _, err := s.CreateConversation(ctx, u, "", "", "", nil, nil, "", nil)
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
	other, _, err := s.CreateConversation(ctx, u, "", "", "", nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, u, other.ID, "cross conversation", nil, nil, nil, first.Message.ID); !errors.Is(err, service.ErrNotFound) {
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
