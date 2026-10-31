package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestLedgerPersistsUsageAndReadOnlyBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := Run{RunID: "r1", AttemptID: "a1", Server: "http://server", DaemonID: "d1", Sender: agentuse.Sender{ID: "alice", Name: "Alice"}, AgentID: "coder", SessionID: "s1", Harness: "codex", Summary: strings.Repeat("中", 220) + "FULL_PROMPT_SECRET", ReceivedAt: time.Now().Add(-time.Minute).UnixMilli()}
	if err = l.Writer().Receive(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = l.Writer().Receive(ctx, run); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = l.Writer().Steer(ctx, "r1", "a1", 7); err != nil {
			t.Fatal(err)
		}
	}
	if err = l.Writer().Complete(ctx, "r1", "a1", Finish{Status: "done", Usage: &agentuse.Usage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 40}, CostUSD: .1, AttachmentCount: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = l.reader.db.ExecContext(ctx, "UPDATE runs SET summary='tampered'"); err == nil {
		t.Fatal("Reader connection allowed a write")
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got, err := l.Reader().Get(ctx, "r1", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || got.SteerCount != 1 || got.AttachmentCount != 2 || got.Usage == nil || got.Usage.InputTokens != 100 || len([]rune(got.Summary)) != 200 || strings.Contains(got.Summary, "SECRET") {
		t.Fatalf("ledger %+v", got)
	}
	if got.DurationMS < 59000 || got.FinishedAt == nil {
		t.Fatal("timing missing", got)
	}
	stats, err := l.Reader().Stats(ctx, "sender", Filter{})
	if err != nil || len(stats) != 1 || stats[0].Runs != 1 || stats[0].Usage.InputTokens != 100 || stats[0].UsageRuns != 1 {
		t.Fatal(stats, err)
	}
	page, err := l.Reader().List(ctx, Filter{Agent: "other"})
	if err != nil || page.Total != 0 {
		t.Fatal(page, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0600 {
		t.Fatalf("ledger permissions %v", st.Mode())
	}
}
func TestLedgerRecoveryRetentionAndAttempts(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -100).UnixMilli()
	for _, attempt := range []string{"a1", "a2"} {
		if err = l.Writer().Receive(ctx, Run{RunID: "same-run", AttemptID: attempt, ReceivedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	if err = l.Writer().Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := l.Reader().List(ctx, Filter{})
	if err != nil || p.Total != 2 || p.Runs[0].Status != "failed" || p.Runs[0].Usage != nil {
		t.Fatal(p, err)
	}
	if err = l.Writer().Purge(ctx, time.Now().AddDate(0, 0, -90)); err != nil {
		t.Fatal(err)
	}
	p, err = l.Reader().List(ctx, Filter{})
	if err != nil || p.Total != 0 {
		t.Fatal(p, err)
	}
}
