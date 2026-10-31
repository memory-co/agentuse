package service

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSessionSnapshotDuringRunCompletion(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, err := s.CreateMultiSession(ctx, u, "", "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 20; i++ {
			if _, e := s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"task"}`), []string{a.ID}, ""); e != nil {
				done <- e
				return
			}
			claim, e := s.ClaimRun(ctx, d, 60)
			if e != nil {
				done <- e
				return
			}
			if e = s.ReportRunEvent(ctx, d.ID, claim.Run.ID, claim.Run.AttemptID, 1, "done", json.RawMessage(`{"ok":true,"output":"done"}`), 60); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	verify := func() {
		t.Helper()
		detail, e := s.ReadSession(ctx, u, ss.ID)
		if e != nil {
			t.Fatal(e)
		}
		ids := map[int64]bool{}
		for _, message := range detail.Messages {
			ids[message.ID] = true
		}
		active := false
		for _, run := range detail.Runs {
			if run.Status == "succeeded" && (run.ResultMessageID == nil || !ids[*run.ResultMessageID]) {
				t.Fatalf("terminal run without result in same snapshot: %+v", run)
			}
			if run.Status == "pending" || run.Status == "running" || run.Status == "waiting" {
				active = true
			}
		}
		if !active && detail.Session.Status != "idle" {
			t.Fatalf("status and runs disagree: %+v", detail.Session)
		}
	}
	for {
		verify()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
			verify()
			return
		default:
		}
	}
}
