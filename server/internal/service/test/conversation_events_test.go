package test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/memory-co/agentuse/internal/testdb"

	"github.com/google/uuid"
)

func TestResultAndReplyRollbackTogether(t *testing.T) {
	s, u, d, a := runFixture(t)
	ctx := context.Background()
	conversation, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "work", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "native", 1, acceptedEvent(run, "one", "work"))
	restore_reject_agent_reply := testdb.RejectWrite(t, s.DB, "messages", "INSERT", "NEW.author_role='agent'", "forced reply failure")
	data, _ := json.Marshal(map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"one"}, "output": "done"})
	envelope := reportEnvelope(delivery, "native", 2, data)
	if err = s.ConversationService.ReportClientEvent(ctx, d.ID, envelope); err == nil {
		t.Fatal("reply failure did not roll back result")
	}
	actual, err := s.RunService.GetRun(ctx, u, conversation.ID, run)
	if err != nil || actual.Status != "running" || actual.ResultMessageID != nil {
		t.Fatal(actual, err)
	}
	restore_reject_agent_reply()
	for i := 0; i < 2; i++ {
		if err = s.ConversationService.ReportClientEvent(ctx, d.ID, envelope); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := s.ConversationService.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 2 || messages[1].Content != "done" {
		t.Fatal("result replay duplicated the reply", messages, err)
	}
}
