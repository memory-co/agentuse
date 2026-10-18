package token

import (
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/models"
)

func TestIssueVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	iss := NewIssuer([]byte("hub-key"))
	iss.now = func() time.Time { return now }

	run := models.Run{ID: "r_118", Platform: "shop", Thread: "issue:MUL-42", Actions: []models.Action{models.ActionReply}}
	tok, err := iss.Issue(run, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := iss.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.RunID != "r_118" || c.Thread != "issue:MUL-42" || !c.Allows(models.ActionReply) || c.Allows(models.ActionStatus) {
		t.Fatalf("claims: %+v", c)
	}

	if _, err := NewIssuer([]byte("other")).Verify(tok); err != ErrInvalid {
		t.Fatalf("wrong key: %v", err)
	}
	iss.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := iss.Verify(tok); err != ErrExpired {
		t.Fatalf("expired: %v", err)
	}
	if _, err := iss.Verify("mat_xxx"); err != ErrMalformed {
		t.Fatalf("malformed: %v", err)
	}
}
