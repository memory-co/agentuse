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
	t.Run("users", func(t *testing.T) { testUsers(t, open(t)) })
	t.Run("agents", func(t *testing.T) { testAgents(t, open(t)) })
	t.Run("connect codes", func(t *testing.T) { testConnectCodes(t, open(t)) })
	t.Run("claim only own device", func(t *testing.T) { testClaimByDevice(t, open(t)) })
	t.Run("fail runs", func(t *testing.T) { testFailRuns(t, open(t)) })
}

var t0 = time.Date(2026, 10, 18, 3, 0, 0, 0, time.UTC)

// 大多数用例只有一个 agent(ag_1),绑在设备 d1 上;d2 也能领是因为测试会把它挪过去。
func run(id, session string, at time.Duration) models.Run {
	return models.Run{ID: id, SessionID: session, Platform: "shop", AgentID: "ag_1", Thread: "issue:" + session,
		Status: models.RunQueued, CreatedAt: t0.Add(at), Actions: []models.Action{models.ActionReply},
		Trigger: models.Comment{ID: "c_" + id, Text: "@claude"}}
}

func claim(ctx context.Context, st store.Store, device string, now time.Time) (models.Run, error) {
	return st.ClaimRun(ctx, store.Claim{DeviceID: device, Now: now, LeaseUntil: now.Add(time.Minute)})
}

func agent(id, handle, device string, at time.Duration) models.Agent {
	return models.Agent{ID: id, Platform: "shop", Handle: handle, Owner: "u_" + handle, AgentUserID: "app_" + handle,
		DeviceID: device, Driver: "claude", Wake: models.WakeOwner, CreatedAt: t0.Add(at)}
}

// bind 建好 ag_1 并绑到 device。
func bind(t *testing.T, st store.Store, device string) {
	t.Helper()
	ctx := context.Background()
	a, err := st.GetAgent(ctx, "ag_1")
	if errors.Is(err, store.ErrNotFound) {
		if err := st.CreateAgent(ctx, agent("ag_1", "alice-claude", device, 0)); err != nil {
			t.Fatal(err)
		}
		return
	}
	a.DeviceID = device
	if err := st.PutAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
}

func testSessions(t *testing.T, st store.Store) {
	ctx := context.Background()
	if _, err := st.SessionByThread(ctx, "shop", "issue:MUL-42", "ag_1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	s := models.Session{ID: "s_1", Platform: "shop", Thread: "issue:MUL-42", AgentID: "ag_1", Branch: "agent/alice-claude/MUL-42", CreatedAt: t0, UpdatedAt: t0}
	if err := st.PutSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	s.AgentSessionID, s.UpdatedAt = "thread_9c1", t0.Add(time.Hour)
	if err := st.PutSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSession(ctx, models.Session{ID: "s_2", Platform: "blog", Thread: "blog:/x", AgentID: "ag_1", UpdatedAt: t0.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	// 同一线程的另一个 agent 是另一段会话。
	if err := st.PutSession(ctx, models.Session{ID: "s_3", Platform: "shop", Thread: "issue:MUL-42", AgentID: "ag_2", UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionByThread(ctx, "shop", "issue:MUL-42", "ag_1")
	if err != nil || got.AgentSessionID != "thread_9c1" || got.Branch != "agent/alice-claude/MUL-42" {
		t.Fatalf("by thread: %+v %v", got, err)
	}
	if got, err := st.SessionByThread(ctx, "shop", "issue:MUL-42", "ag_2"); err != nil || got.ID != "s_3" {
		t.Fatalf("by thread, other agent: %+v %v", got, err)
	}
	if got, err := st.GetSession(ctx, "s_2"); err != nil || got.Thread != "blog:/x" {
		t.Fatalf("get: %+v %v", got, err)
	}
	all, err := st.ListSessions(ctx, "", 0)
	if err != nil || len(all) != 3 || all[0].ID != "s_1" {
		t.Fatalf("list all (newest first): %+v %v", all, err)
	}
	if blog, _ := st.ListSessions(ctx, "blog", 0); len(blog) != 1 || blog[0].ID != "s_2" {
		t.Fatalf("list blog: %+v", blog)
	}
}

func testClaim(t *testing.T, st store.Store) {
	ctx := context.Background()
	bind(t, st, "d1")
	for _, r := range []models.Run{run("r1", "sA", 0), run("r2", "sA", 1), run("r3", "sB", 2)} {
		if err := st.CreateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	a, err := claim(ctx, st, "d1", t0)
	if err != nil || a.ID != "r1" || a.Status != models.RunClaimed || a.Attempts != 1 || a.DeviceID != "d1" || a.LeaseUntil == nil {
		t.Fatalf("first claim: %+v %v", a, err)
	}
	if a.Trigger.ID != "c_r1" || len(a.Actions) != 1 {
		t.Fatalf("payload lost: %+v", a)
	}
	// r2 和 r1 同一会话,r1 还在跑,所以先领到 r3。
	b, err := claim(ctx, st, "d1", t0)
	if err != nil || b.ID != "r3" {
		t.Fatalf("second claim should skip busy session: %+v %v", b, err)
	}
	if _, err := claim(ctx, st, "d1", t0); !errors.Is(err, store.ErrNotFound) {
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
	bind(t, st, "d1")
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
	bind(t, st, "d2") // agent 搬到了另一台设备
	r, err = claim(ctx, st, "d2", t0.Add(4*time.Minute))
	if err != nil || r.Attempts != 2 || r.DeviceID != "d2" {
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
	bind(t, st, "d1")
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

func testUsers(t *testing.T, st store.Store) {
	ctx := context.Background()
	if _, err := st.GetUser(ctx, "shop", "alice"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	st.PutUser(ctx, models.User{Platform: "shop", ID: "alice", Name: "Alice", CreatedAt: t0, UpdatedAt: t0})
	st.PutUser(ctx, models.User{Platform: "shop", ID: "alice", Name: "Alice Z", CreatedAt: t0.Add(time.Hour), UpdatedAt: t0.Add(time.Hour)})
	u, err := st.GetUser(ctx, "shop", "alice")
	if err != nil || u.Name != "Alice Z" || !u.CreatedAt.Equal(t0) {
		t.Fatalf("user upsert keeps created_at: %+v %v", u, err)
	}
	if _, err := st.GetUser(ctx, "blog", "alice"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("users are per platform: %v", err)
	}
}

func testAgents(t *testing.T, st store.Store) {
	ctx := context.Background()
	for _, a := range []models.Agent{agent("ag_2", "bob-claude", "", time.Minute), agent("ag_1", "alice-claude", "d1", 0)} {
		if err := st.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateAgent(ctx, agent("ag_3", "alice-claude", "", 0)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate handle: %v", err)
	}
	other := agent("ag_4", "alice-claude", "", 0)
	other.Platform = "blog"
	if err := st.CreateAgent(ctx, other); err != nil {
		t.Fatalf("same handle on another platform: %v", err)
	}
	if a, err := st.AgentByHandle(ctx, "shop", "bob-claude"); err != nil || a.ID != "ag_2" {
		t.Fatalf("by handle: %+v %v", a, err)
	}
	all, _ := st.ListAgents(ctx, store.AgentFilter{Platform: "shop"})
	if len(all) != 2 || all[0].ID != "ag_1" {
		t.Fatalf("list oldest first: %+v", all)
	}
	if l, _ := st.ListAgents(ctx, store.AgentFilter{DeviceID: "d1"}); len(l) != 1 || l[0].ID != "ag_1" {
		t.Fatalf("by device: %+v", l)
	}
	if l, _ := st.ListAgents(ctx, store.AgentFilter{Platform: "shop", AgentUserID: "app_bob-claude"}); len(l) != 1 || l[0].ID != "ag_2" {
		t.Fatalf("by agent user: %+v", l)
	}
	b, _ := st.GetAgent(ctx, "ag_2")
	b.DeviceID, b.Workdir = "d2", "/home/bob/repo"
	if err := st.PutAgent(ctx, b); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.ListAgents(ctx, store.AgentFilter{DeviceID: "d2"}); len(l) != 1 || l[0].Workdir != "/home/bob/repo" {
		t.Fatalf("update: %+v", l)
	}
	if err := st.PutAgent(ctx, agent("ag_x", "x", "", 0)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("put missing agent: %v", err)
	}
	st.PutConnectCode(ctx, models.ConnectCode{Hash: "h2", AgentID: "ag_2", ExpiresAt: t0.Add(time.Hour)})
	if err := st.DeleteAgent(ctx, "ag_2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAgent(ctx, "ag_2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	if _, err := st.TakeConnectCode(ctx, "h2", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("code of deleted agent: %v", err)
	}
	if err := st.DeleteAgent(ctx, "ag_2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	d := models.Device{ID: "d1", Platform: "shop", Owner: "u_alice-claude", Name: "alice-mbp", KeyHash: "kh", CreatedAt: t0}
	if err := st.PutDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	d.RevokedAt = &t0
	st.PutDevice(ctx, d)
	if got, err := st.GetDevice(ctx, "d1"); err != nil || got.RevokedAt == nil || got.KeyHash != "kh" {
		t.Fatalf("device: %+v %v", got, err)
	}
}

func testConnectCodes(t *testing.T, st store.Store) {
	ctx := context.Background()
	st.PutConnectCode(ctx, models.ConnectCode{Hash: "h1", Platform: "shop", AgentID: "ag_1", ExpiresAt: t0.Add(15 * time.Minute)})
	// 新码作废同一个 agent 的旧码。
	st.PutConnectCode(ctx, models.ConnectCode{Hash: "h2", Platform: "shop", AgentID: "ag_1", ExpiresAt: t0.Add(15 * time.Minute)})
	st.PutConnectCode(ctx, models.ConnectCode{Hash: "h3", Platform: "shop", AgentID: "ag_2", ExpiresAt: t0.Add(15 * time.Minute)})
	if _, err := st.TakeConnectCode(ctx, "h1", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("superseded code: %v", err)
	}
	c, err := st.TakeConnectCode(ctx, "h2", t0)
	if err != nil || c.AgentID != "ag_1" {
		t.Fatalf("take: %+v %v", c, err)
	}
	if _, err := st.TakeConnectCode(ctx, "h2", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second take: %v", err)
	}
	if _, err := st.TakeConnectCode(ctx, "h3", t0.Add(15*time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := st.TakeConnectCode(ctx, "h3", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired code is consumed too: %v", err)
	}
}

// bob 的设备领不到 alice-claude 的运行;没绑设备的 agent 的运行谁也领不到。
func testClaimByDevice(t *testing.T, st store.Store) {
	ctx := context.Background()
	st.CreateAgent(ctx, agent("ag_1", "alice-claude", "d_alice", 0))
	st.CreateAgent(ctx, agent("ag_2", "bob-claude", "d_bob", time.Second))
	st.CreateAgent(ctx, agent("ag_3", "carol-claude", "", 2*time.Second))
	r1, r2, r3 := run("r1", "sA", 0), run("r2", "sB", 1), run("r3", "sC", 2)
	r2.AgentID, r3.AgentID = "ag_2", "ag_3"
	for _, r := range []models.Run{r1, r2, r3} {
		st.CreateRun(ctx, r)
	}
	if r, err := claim(ctx, st, "d_bob", t0); err != nil || r.ID != "r2" {
		t.Fatalf("bob claims his own: %+v %v", r, err)
	}
	if _, err := claim(ctx, st, "d_bob", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bob claimed someone else's run: %v", err)
	}
	if _, err := claim(ctx, st, "", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty device claimed an unbound agent's run: %v", err)
	}
	if r, err := claim(ctx, st, "d_alice", t0); err != nil || r.ID != "r1" {
		t.Fatalf("alice: %+v %v", r, err)
	}
}

func testFailRuns(t *testing.T, st store.Store) {
	ctx := context.Background()
	bind(t, st, "d1")
	r2 := run("r2", "sB", time.Hour)
	r3 := run("r3", "sC", 0)
	r3.AgentID = "ag_9"
	for _, r := range []models.Run{run("r1", "sA", 0), r2, r3} {
		st.CreateRun(ctx, r)
	}
	claim(ctx, st, "d1", t0) // r1 claimed
	now := t0.Add(2 * time.Hour)

	// 排队超时:只动排队中、创建早于截止时间的。
	got, err := st.FailRuns(ctx, store.FailFilter{Platform: "shop", CreatedBefore: t0.Add(time.Minute), Statuses: []models.RunStatus{models.RunQueued}}, "queue_ttl", now)
	if err != nil || len(got) != 1 || got[0].ID != "r3" || got[0].Status != models.RunFailed || got[0].Error != "queue_ttl" {
		t.Fatalf("expire queued: %+v %v", got, err)
	}
	// 删 agent:它的排队中和进行中的都判失败。
	got, err = st.FailRuns(ctx, store.FailFilter{AgentID: "ag_1", Statuses: []models.RunStatus{models.RunQueued, models.RunClaimed}}, "agent_deleted", now)
	if err != nil || len(got) != 2 {
		t.Fatalf("fail agent runs: %+v %v", got, err)
	}
	if r, _ := st.GetRun(ctx, "r1"); r.Status != models.RunFailed || r.LeaseUntil != nil || r.FinishedAt == nil {
		t.Fatalf("claimed run failed: %+v", r)
	}
	if _, err := st.FinishRun(ctx, "r1", "d1", 1, models.RunSucceeded, "", now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("finish a failed run: %v", err)
	}
	if got, _ := st.FailRuns(ctx, store.FailFilter{Statuses: []models.RunStatus{models.RunQueued}}, "x", now); len(got) != 0 {
		t.Fatalf("nothing left: %+v", got)
	}
}
