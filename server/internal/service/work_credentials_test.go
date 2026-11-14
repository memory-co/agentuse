package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestWorkCredentialsFollowTheCurrentCoordinator(t *testing.T) {
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
