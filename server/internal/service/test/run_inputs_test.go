package test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
)

func TestRunInputsRecordOnlyAcceptedTargetsOfAMultiAgentMessage(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	b, err := s.AgentService.CreateAgent(ctx, u.ID, "reviewer", "Reviewer", d.ID, a.Harness, "")
	if err != nil {
		t.Fatal(err)
	}
	conversation, reply, err := s.ConversationService.CreateConversation(ctx, u, "", "", "review", []string{a.ID, b.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.RunInputs) != 0 || len(reply.Runs) != 0 {
		t.Fatal("message creation allocated execution facts", reply)
	}
	first, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := s.RunService.RunInputs(ctx, conversation.ID)
	if err != nil || len(inputs) != 0 {
		t.Fatal("dispatch allocated inputs", inputs, err)
	}
	if err = s.RunService.RejectMessage(ctx, d.ID, second.Key(), protocol.AckError, "init failed"); err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, first, "session", 1, acceptedEvent(run, "input", first.Content))
	detail, err := s.ConversationService.ReadConversation(ctx, u, conversation.ID)
	if err != nil || len(detail.RunInputs) != 1 || detail.RunInputs[0].MessageID != reply.Message.ID || detail.RunInputs[0].RunID != run {
		t.Fatal(detail, err)
	}
	if receipt, ok := detail.Messages[0].AgentErrors[second.AgentID]; !ok || receipt.Error != "init failed" || receipt.Uncertain {
		t.Fatal("rejection did not remain on its source message", detail)
	}
	if detail.MessageSettled(reply.Message.ID) {
		t.Fatal("accepted but running input was treated as complete")
	}
	reportNative(t, s, d, first, "session", 2, map[string]any{"type": "result", "run_id": run, "input_ids": []string{"input"}, "status": "completed", "output": "done"})
	detail, err = s.ConversationService.ReadConversation(ctx, u, conversation.ID)
	if err != nil || !detail.MessageSettled(reply.Message.ID) || len(detail.RunInputs) != 1 || len(detail.Runs) != 1 || detail.Rounds[0].FinishedAt == nil {
		t.Fatal(detail, err)
	}
}

func TestUnknownReceiptCreatesNoInputAndAcceptanceUnblocksSupplements(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	conversation, first, err := s.ConversationService.CreateConversation(ctx, u, "", "", "first", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RunService.RejectMessage(ctx, d.ID, p.Key(), protocol.AckUnknown, "receipt lost"); err != nil {
		t.Fatal(err)
	}
	second, err := s.ConversationService.SendMessage(ctx, u, conversation.ID, "supplement", []string{a.ID}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunService.DispatchMessage(ctx, d); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("uncertain send did not block its binding", err)
	}
	detail, err := s.ConversationService.ReadConversation(ctx, u, conversation.ID)
	if err != nil || len(detail.RunInputs) != 0 || len(detail.Runs) != 0 || !detail.Messages[0].AgentErrors[a.ID].Uncertain || detail.MessageSettled(first.Message.ID) || detail.Rounds[0].FinishedAt != nil {
		t.Fatal(detail, err)
	}
	run := uuid.NewString()
	event := reportNative(t, s, d, p, "session", 1, acceptedEvent(run, "input-1", p.Content))
	if err = s.ConversationService.ReportClientEvent(ctx, d.ID, event); err != nil {
		t.Fatal(err)
	}
	p2, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil || p2.MessageID != second.Message.ID {
		t.Fatal(p2, err)
	}
	reportNative(t, s, d, p2, "session", 2, acceptedEvent(run, "input-2", p2.Content))
	detail, err = s.ConversationService.ReadConversation(ctx, u, conversation.ID)
	if err != nil || len(detail.RunInputs) != 2 || len(detail.Runs) != 1 || len(detail.Messages[0].AgentErrors) != 0 {
		t.Fatal("acceptance failed to clear uncertainty or duplicated a run", detail, err)
	}
	reportNative(t, s, d, p2, "session", 3, map[string]any{"type": "result", "run_id": run, "input_ids": []string{"input-1", "input-2"}, "status": "completed", "output": "done"})
	detail, err = s.ConversationService.ReadConversation(ctx, u, conversation.ID)
	if err != nil || !detail.MessageSettled(first.Message.ID) || !detail.MessageSettled(second.Message.ID) {
		t.Fatal(detail, err)
	}
}
