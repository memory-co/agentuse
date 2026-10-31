package service

import (
	"context"
	"encoding/json"
	"testing"
)

type observingNotifier struct {
	discardNotifier
	kick   func()
	steers func([]Delivery)
}

func (n observingNotifier) KickAll() {
	if n.kick != nil {
		n.kick()
	}
}
func (n observingNotifier) PushRunSteers(ds []Delivery) {
	if n.steers != nil {
		n.steers(ds)
	}
}

func TestMessageNotificationAfterCommit(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, err := s.CreateMultiSession(ctx, u, "", "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	kicks := 0
	s.SetNotifier(observingNotifier{kick: func() {
		kicks++
		// A query from the callback would block with the single database connection
		// if the transaction had not already committed.
		messages, e := s.Messages(ctx, ss.ID)
		if e != nil || len(messages) == 0 {
			t.Errorf("notification before committed message: %v %v", messages, e)
		}
	}})
	if _, err = s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"work"}`), []string{a.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if kicks != 1 {
		t.Fatalf("kicks=%d", kicks)
	}
	if _, err = s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"invalid"}`), []string{"missing"}, ""); err == nil {
		t.Fatal("accepted invalid target")
	}
	if kicks != 1 {
		t.Fatal("notified rejected transaction")
	}
	cl, err := s.ClaimRun(ctx, d, 60)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReportRunEvent(ctx, d.ID, cl.Run.ID, cl.Run.AttemptID, 1, "started", json.RawMessage(`{}`), 60); err != nil {
		t.Fatal(err)
	}
	steers := 0
	s.SetNotifier(observingNotifier{steers: func(ds []Delivery) { steers += len(ds) }})
	if _, err = s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"steer"}`), []string{a.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if steers != 1 {
		t.Fatalf("steers=%d", steers)
	}
}
