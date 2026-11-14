package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestWorkSummaryRetriesAfterCoordinatorUnavailable(t *testing.T) {
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
	s.DB.QueryRow(`SELECT count(*) FROM run_notifications WHERE message_id=?`, first.Message.ID).Scan(&notified)
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
