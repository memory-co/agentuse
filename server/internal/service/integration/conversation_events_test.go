package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestResultAndReplyRollbackTogether(t *testing.T) {
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
	if _, err = s.DB.Exec(`CREATE TRIGGER reject_agent_reply BEFORE INSERT ON messages WHEN NEW.author_role='agent' BEGIN SELECT RAISE(FAIL,'forced reply failure'); END`); err != nil {
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
	if _, err = s.DB.Exec(`DROP TRIGGER reject_agent_reply`); err != nil {
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
