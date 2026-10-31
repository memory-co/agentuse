package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func runStore(t *testing.T) (*Service, User, Daemon, Agent) {
	t.Helper()
	s := newStore(t)
	s.now = func() time.Time { return time.Unix(100000, 0) }
	ctx := context.Background()
	u, _, e := s.CreateUser(ctx, "u_run", "runner", RoleUser)
	if e != nil {
		t.Fatal(e)
	}
	d := registered(t, s, u, "machine", "claude")
	if _, e = s.DaemonConnected(ctx, d, []ReportedHarness{{Name: "claude"}}, []string{RunCapability, "attachments-v1"}); e != nil {
		t.Fatal(e)
	}
	a, e := s.CreateAgent(ctx, u.ID, "coder", "Coder", d.ID, "claude", "")
	if e != nil {
		t.Fatal(e)
	}
	return s, u, d, a
}
func TestRunConcurrentMessages(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, err := s.CreateMultiSession(ctx, u, "", "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"supplement"}`), []string{a.ID}, "")
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	runs, err := s.Runs(ctx, ss.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal("duplicate active run", runs, err)
	}
	cl, err := s.ClaimRun(ctx, d, 60)
	if err != nil || len(cl.TaskMessageIDs) != 10 || len(cl.Messages) != 10 {
		t.Fatal("pending tasks lost", cl, err)
	}
}
func TestRunLeaseRecoversQuestionAnswer(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	now := time.Unix(100000, 0)
	s.now = func() time.Time { return now }
	ss, _, err := s.CreateMultiSession(ctx, u, "", "", json.RawMessage(`{"prompt":"task"}`), []string{a.ID}, "")
	if err != nil {
		t.Fatal(err)
	}
	cl, err := s.ClaimRun(ctx, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"tool_use_id":"q1","questions":[{"question":"language?"}]}`)
	if err = s.ReportRunEvent(ctx, d.ID, cl.Run.ID, cl.Run.AttemptID, 1, "question", payload, 10); err != nil {
		t.Fatal(err)
	}
	qs, err := s.RunQuestions(ctx, ss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AnswerRunQuestion(ctx, u, ss.ID, cl.Run.ID, qs[0].ID, map[string]string{"language?": "Go"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Second)
	if err = s.RequeueRuns(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimRun(ctx, d, 60)
	if err != nil || next.Run.ID != cl.Run.ID || next.Run.AttemptID == cl.Run.AttemptID || len(next.TaskMessageIDs) != 2 {
		t.Fatal("requeue", next, err)
	}
	found := false
	for _, m := range next.Messages {
		if strings.Contains(string(m.Content), "language? → Go") {
			found = true
		}
	}
	if !found {
		t.Fatal("answer lost after lease expiry")
	}
	if err = s.ReportRunEvent(ctx, d.ID, cl.Run.ID, cl.Run.AttemptID, 1, "question", payload, 60); err != ErrStaleMessage {
		t.Fatal("stale event replay accepted", err)
	}
}

func TestRejectOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE sessions(id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if st, err := Open(path); err == nil {
		st.Close()
		t.Fatal("old schema must be explicitly reset")
	}
}

func TestSharedSchemaAndSessionLifecycle(t *testing.T) {
	s, u, _, _ := runStore(t)
	ctx := context.Background()
	for _, table := range []string{"steers", "questions", "session_events", "work_tokens", "session_access"} {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("obsolete table %s: %d %v", table, n, err)
		}
	}
	for _, col := range []string{"agent_id", "daemon_id", "harness", "harness_session_id", "lease_until"} {
		var n int
		s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name=?", col).Scan(&n)
		if n != 0 {
			t.Fatalf("session runtime column %s", col)
		}
	}
	ss, _, err := s.CreateMultiSession(ctx, u, "", "notes", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveSession(ctx, u, ss.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReopenSession(ctx, u, ss.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSession(ctx, u, ss.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetSession(ctx, u, ss.ID); err != ErrNotFound {
		t.Fatal("deleted session", err)
	}
}
