// Package storetest 是 store.Store 的行为测试,每种实现都跑一遍。
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/internal/store"
)

// Run 对 open 交回的新存储跑全部用例。
func Run(t *testing.T, open func(t *testing.T) store.Store) {
	t.Run("sessions", func(t *testing.T) { testSessions(t, open(t)) })
	t.Run("claim order and per-session serialization", func(t *testing.T) { testClaim(t, open(t)) })
	t.Run("lease, finish and reap", func(t *testing.T) { testLease(t, open(t)) })
	t.Run("list runs", func(t *testing.T) { testListRuns(t, open(t)) })
}

var t0 = time.Date(2026, 10, 18, 3, 0, 0, 0, time.UTC)

func run(id, session string, at time.Duration) models.Run {
	return models.Run{ID: id, SessionID: session, Platform: "shop", Thread: "issue:" + session,
		Status: models.RunQueued, CreatedAt: t0.Add(at), Actions: []models.Action{models.ActionReply},
		Trigger: models.Comment{ID: "c_" + id, Text: "@claude"}}
}

func claim(ctx context.Context, st store.Store, daemon string, now time.Time) (models.Run, error) {
	return st.ClaimRun(ctx, store.Claim{DaemonID: daemon, Now: now, LeaseUntil: now.Add(time.Minute)})
}

func testSessions(t *testing.T, st store.Store) {
	ctx := context.Background()
	if _, err := st.SessionByThread(ctx, "shop", "issue:MUL-42"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	s := models.Session{ID: "s_1", Platform: "shop", Thread: "issue:MUL-42", Branch: "agent/claude/MUL-42", CreatedAt: t0, UpdatedAt: t0}
	if err := st.PutSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	s.AgentSessionID, s.UpdatedAt = "thread_9c1", t0.Add(time.Hour)
	if err := st.PutSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSession(ctx, models.Session{ID: "s_2", Platform: "blog", Thread: "blog:/x", UpdatedAt: t0.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionByThread(ctx, "shop", "issue:MUL-42")
	if err != nil || got.AgentSessionID != "thread_9c1" || got.Branch != "agent/claude/MUL-42" {
		t.Fatalf("by thread: %+v %v", got, err)
	}
	if got, err := st.GetSession(ctx, "s_2"); err != nil || got.Thread != "blog:/x" {
		t.Fatalf("get: %+v %v", got, err)
	}
	all, err := st.ListSessions(ctx, "", 0)
	if err != nil || len(all) != 2 || all[0].ID != "s_1" {
		t.Fatalf("list all (newest first): %+v %v", all, err)
	}
	if blog, _ := st.ListSessions(ctx, "blog", 0); len(blog) != 1 || blog[0].ID != "s_2" {
		t.Fatalf("list blog: %+v", blog)
	}
}

func testClaim(t *testing.T, st store.Store) {
	ctx := context.Background()
	for _, r := range []models.Run{run("r1", "sA", 0), run("r2", "sA", 1), run("r3", "sB", 2)} {
		if err := st.CreateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	a, err := claim(ctx, st, "d1", t0)
	if err != nil || a.ID != "r1" || a.Status != models.RunClaimed || a.Attempts != 1 || a.DaemonID != "d1" || a.LeaseUntil == nil {
		t.Fatalf("first claim: %+v %v", a, err)
	}
	if a.Trigger.ID != "c_r1" || len(a.Actions) != 1 {
		t.Fatalf("payload lost: %+v", a)
	}
	// r2 和 r1 同一会话,r1 还在跑,所以先领到 r3。
	b, err := claim(ctx, st, "d2", t0)
	if err != nil || b.ID != "r3" {
		t.Fatalf("second claim should skip busy session: %+v %v", b, err)
	}
	if _, err := claim(ctx, st, "d2", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("third claim: %v", err)
	}
	if _, err := st.FinishRun(ctx, "r1", "d1", 1, models.RunSucceeded, "", t0); err != nil {
		t.Fatal(err)
	}
	c, err := claim(ctx, st, "d1", t0)
	if err != nil || c.ID != "r2" {
		t.Fatalf("after finish: %+v %v", c, err)
	}
}

func testLease(t *testing.T, st store.Store) {
	ctx := context.Background()
	if err := st.CreateRun(ctx, run("r1", "sA", 0)); err != nil {
		t.Fatal(err)
	}
	r, _ := claim(ctx, st, "d1", t0) // lease t0+1m
	if _, err := st.ExtendLease(ctx, "r1", "d2", 1, t0.Add(time.Hour)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("other daemon extend: %v", err)
	}
	if _, err := st.ExtendLease(ctx, "nope", "d1", 1, t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	r, err := st.ExtendLease(ctx, "r1", "d1", 1, t0.Add(2*time.Minute))
	if err != nil || !r.LeaseUntil.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("extend: %+v %v", r, err)
	}
	if reaped, _ := st.ReapExpired(ctx, t0.Add(90*time.Second), 2); len(reaped) != 0 {
		t.Fatalf("reaped a live lease: %+v", reaped)
	}
	reaped, err := st.ReapExpired(ctx, t0.Add(3*time.Minute), 2)
	if err != nil || len(reaped) != 1 || reaped[0].Status != models.RunQueued {
		t.Fatalf("reap back to queue: %+v %v", reaped, err)
	}
	// 被回收后,旧的那次领取不能再交回。
	if _, err := st.FinishRun(ctx, "r1", "d1", 1, models.RunSucceeded, "", t0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("finish stale attempt: %v", err)
	}
	r, err = claim(ctx, st, "d2", t0.Add(4*time.Minute))
	if err != nil || r.Attempts != 2 || r.DaemonID != "d2" {
		t.Fatalf("reclaim: %+v %v", r, err)
	}
	reaped, _ = st.ReapExpired(ctx, t0.Add(time.Hour), 2)
	if len(reaped) != 1 || reaped[0].Status != models.RunFailed || reaped[0].FinishedAt == nil {
		t.Fatalf("max attempts → failed: %+v", reaped)
	}
	got, _ := st.GetRun(ctx, "r1")
	if got.Status != models.RunFailed || got.Error == "" {
		t.Fatalf("persisted failure: %+v", got)
	}
}

func testListRuns(t *testing.T, st store.Store) {
	ctx := context.Background()
	for _, r := range []models.Run{run("r1", "sA", 0), run("r2", "sB", 1), run("r3", "sA", 2)} {
		if err := st.CreateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	claim(ctx, st, "d1", t0)
	all, err := st.ListRuns(ctx, store.RunFilter{})
	if err != nil || len(all) != 3 || all[0].ID != "r3" {
		t.Fatalf("list newest first: %+v %v", all, err)
	}
	if q, _ := st.ListRuns(ctx, store.RunFilter{Status: models.RunQueued}); len(q) != 2 {
		t.Fatalf("queued: %+v", q)
	}
	if s, _ := st.ListRuns(ctx, store.RunFilter{SessionID: "sA", Limit: 1}); len(s) != 1 || s[0].ID != "r3" {
		t.Fatalf("by session with limit: %+v", s)
	}
}
