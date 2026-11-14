package test

import (
	"context"
	"fmt"
	"testing"
)

func TestBrowserChangesCommitFailureDeletionAndBoundedQueue(t *testing.T) {
	s, u, _, _ := runStore(t)
	sub, unsubscribe := s.BrowserChanges.Subscribe(u)
	defer unsubscribe()
	ctx := context.Background()
	ss, _, err := s.CreateConversation(ctx, u, "", "notice", "", nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if changes := sub.Drain(); len(changes) != 1 || changes[0].ID != ss.ID {
		t.Fatal(changes)
	}
	if _, err = s.SendMessage(ctx, u, ss.ID, "bad", []string{"missing-agent"}, nil, nil, ""); err == nil {
		t.Fatal("expected rejection")
	}
	if changes := sub.Drain(); len(changes) != 0 {
		t.Fatal("failed mutation notified", changes)
	}
	if err = s.DeleteConversation(ctx, u, ss.ID); err != nil {
		t.Fatal(err)
	}
	if changes := sub.Drain(); len(changes) != 1 || changes[0].ID != ss.ID {
		t.Fatal("deletion scope lost", changes)
	}
	for i := 0; i < 200; i++ {
		s.NotifyResource("agents", fmt.Sprint(i), u.ID)
	}
	if changes := sub.Drain(); len(changes) != 1 || changes[0].Resource != "reset" {
		t.Fatal("queue did not collapse to reset", changes)
	}
	unsubscribe()
	s.NotifyResource("agents", "ignored", u.ID)
	if changes := sub.Drain(); len(changes) != 0 {
		t.Fatal("unsubscribed listener retained", changes)
	}
}
