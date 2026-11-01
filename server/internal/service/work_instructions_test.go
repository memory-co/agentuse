package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClaimRunOwnsWorkInstructions(t *testing.T) {
	for _, kind := range []string{"ordinary", "coordinator", "other-agent", "thread"} {
		t.Run(kind, func(t *testing.T) {
			s, u, d, a := runStore(t)
			ctx := context.Background()
			var session Session
			var err error
			if kind == "ordinary" {
				session, _, err = s.CreateMultiSession(ctx, u, "", "", nil, nil, "")
			} else {
				var work Work
				work, session, err = s.CreateMultiWork(ctx, u, "work", a.ID, "shared work instructions")
				if err != nil {
					t.Fatal(err)
				}
				if kind == "other-agent" {
					a, err = s.CreateAgent(ctx, u.ID, "reviewer", "Reviewer", d.ID, "claude", "")
				}
				if kind == "thread" {
					session, _, err = s.CreateMultiSession(ctx, u, work.ID, "thread", nil, nil, "")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.SendMultiMessage(ctx, u, session.ID, json.RawMessage(`{"prompt":"task"}`), []string{a.ID}, ""); err != nil {
				t.Fatal(err)
			}
			claim, err := s.ClaimRun(ctx, d, 60)
			if err != nil {
				t.Fatal(err)
			}
			coordinator := kind == "coordinator"
			if strings.Contains(claim.Instructions, coordinatorGuide) != coordinator {
				t.Fatalf("wrong policy for %s: %q", kind, claim.Instructions)
			}
			if (claim.WorkToken != "") != coordinator {
				t.Fatal("wrong work authorization scope")
			}
			if kind == "ordinary" && claim.Instructions != "" {
				t.Fatal("ordinary task received work policy")
			}
			if kind != "ordinary" && !strings.Contains(claim.Instructions, "shared work instructions") {
				t.Fatal("work instructions lost")
			}
		})
	}
}
