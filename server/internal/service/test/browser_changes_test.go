package test

import (
	"context"
	"fmt"
	"testing"

	"github.com/memory-co/agentuse/server/internal/notification"
)

func TestBrowserChangesCommitFailureDeletionAndBoundedQueue(t *testing.T) {
	s, u, _, _ := runStore(t)
	sub, unsubscribe := s.BrowserChanges.Subscribe(u.ID, u.IsAdmin())
	defer unsubscribe()
	ctx := context.Background()
	ss, _, err := s.ConversationService.CreateConversation(ctx, u, "", "notice", "", nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if changes := sub.Drain(); len(changes) != 1 || changes[0].ID != ss.ID {
		t.Fatal(changes)
	}
	if _, err = s.ConversationService.SendMessage(ctx, u, ss.ID, "bad", []string{"missing-agent"}, nil, nil, ""); err == nil {
		t.Fatal("expected rejection")
	}
	if changes := sub.Drain(); len(changes) != 0 {
		t.Fatal("failed mutation notified", changes)
	}
	if err = s.ConversationService.DeleteConversation(ctx, u, ss.ID); err != nil {
		t.Fatal(err)
	}
	if changes := sub.Drain(); len(changes) != 1 || changes[0].ID != ss.ID {
		t.Fatal("deletion scope lost", changes)
	}
	for i := 0; i < 200; i++ {
		s.BrowserChanges.Publish(notification.Change{Resource: "agents", ID: fmt.Sprint(i)}, u.ID)
	}
	if changes := sub.Drain(); len(changes) != 1 || changes[0].Resource != "reset" {
		t.Fatal("queue did not collapse to reset", changes)
	}
	unsubscribe()
	s.BrowserChanges.Publish(notification.Change{Resource: "agents", ID: "ignored"}, u.ID)
	if changes := sub.Drain(); len(changes) != 0 {
		t.Fatal("unsubscribed listener retained", changes)
	}
}
