package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestFileLedgerHierarchyAndRestart(t *testing.T) {
	root := t.TempDir()
	l, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s, workspace, e := l.AcquireSession(ctx, "https---one.example", "conversation", "coder", "codex", "v1")
	if e != nil {
		t.Fatal(e)
	}
	w := l.ForEndpoint(s.EndpointID)
	r := Run{EndpointID: s.EndpointID, ConversationID: s.ConversationID, SessionID: s.ID, RunID: "run", AttemptID: "attempt", Harness: "codex", Summary: strings.Repeat("中", 220) + "SECRET"}
	if e = w.Receive(ctx, r); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = w.Steer(ctx, "run", "attempt", 7); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Complete(ctx, "run", "attempt", Finish{Status: "succeeded", Output: "answer", Usage: &agentuse.Usage{InputTokens: 100, OutputTokens: 20}}); e != nil {
		t.Fatal(e)
	}
	if e = l.Confirm(ctx, s.EndpointID, "run", "attempt"); e != nil {
		t.Fatal(e)
	}
	if e = l.ReleaseSession(s, 9, true, "run", "attempt"); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(filepath.Dir(workspace), "sessions", s.ID, "runs", "run", "attempt", "run.json")
	if st, e := os.Stat(path); e != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, e)
	}
	l, _ = Open(root)
	got, e := l.Reader().Detail(ctx, s.EndpointID, s.ConversationID, s.ID, "run", "attempt")
	if e != nil || got.Output != "answer" || got.SteerCount != 1 || len([]rune(got.Summary)) != 200 || strings.Contains(got.Summary, "SECRET") {
		t.Fatal(got, e)
	}
	same, cwd, e := l.AcquireSession(ctx, s.EndpointID, s.ConversationID, "coder", "codex", "v1")
	if e != nil || same.ID != s.ID || cwd != workspace {
		t.Fatal(same, cwd, e)
	}
	other, cwd, e := l.AcquireSession(ctx, s.EndpointID, s.ConversationID, "reviewer", "claude", "v1")
	if e != nil || other.ID == s.ID || cwd != workspace {
		t.Fatal(other, cwd, e)
	}
	if _, _, e = l.AcquireSession(ctx, s.EndpointID, s.ConversationID, "coder", "codex", "v1"); e == nil {
		t.Fatal("busy session reused")
	}
	if _, e = l.Reader().List(ctx, Filter{}); !errors.Is(e, ErrInvalid) {
		t.Fatal("unscoped query allowed", e)
	}
	if _, e = l.Reader().Detail(ctx, s.EndpointID, "../escape", s.ID, "run", "attempt"); !errors.Is(e, ErrInvalid) {
		t.Fatal("path traversal", e)
	}
}
func TestEndpointIsolationAndQuota(t *testing.T) {
	l, _ := Open(t.TempDir())
	ctx := context.Background()
	var first Session
	for _, ep := range []string{"https---one", "https---two"} {
		s, dir, e := l.AcquireSession(ctx, ep, "same", "agent", "custom", "v1")
		if e != nil {
			t.Fatal(e)
		}
		if first.ID == "" {
			first = s
		}
		os.WriteFile(filepath.Join(dir, "large"), make([]byte, 4096), 0600)
		w := l.ForEndpoint(ep)
		if e = w.Receive(ctx, Run{EndpointID: ep, ConversationID: "same", SessionID: s.ID, RunID: "same", AttemptID: "same"}); e != nil {
			t.Fatal(e)
		}
		if e = w.Complete(ctx, "same", "same", Finish{Status: "succeeded", Output: ep}); e != nil {
			t.Fatal(e)
		}
		if e = l.Confirm(ctx, ep, "same", "same"); e != nil {
			t.Fatal(e)
		}
		if ep == "https---two" {
			if e = l.ReleaseSession(s, 0, true, "same", "same"); e != nil {
				t.Fatal(e)
			}
		}
	}
	_, e := l.Reclaim(ctx, 1)
	if e == nil {
		t.Fatal("quota should block with in-use workspace")
	}
	if _, e = l.Reader().Session(ctx, "https---one", "same", first.ID); e != nil {
		t.Fatal("active conversation removed", e)
	}
	cs, e := l.Reader().Conversations(ctx, "https---two")
	if e != nil || len(cs) != 0 {
		t.Fatal("idle conversation not removed", cs, e)
	}
	external := t.TempDir()
	os.WriteFile(filepath.Join(external, "secret"), []byte("safe"), 0600)
	if e = os.Symlink(external, filepath.Join(l.Root, "linked")); e != nil {
		t.Fatal(e)
	}
	if _, e = l.Reader().Conversations(ctx, "linked"); e == nil {
		t.Fatal("symlink root followed")
	}
	if b, _ := os.ReadFile(filepath.Join(external, "secret")); string(b) != "safe" {
		t.Fatal("external data touched")
	}
}
func TestPendingResultSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l, _ := Open(root)
	s, _, e := l.AcquireSession(ctx, "ep", "c", "a", "custom", "v")
	if e != nil {
		t.Fatal(e)
	}
	w := l.ForEndpoint("ep")
	w.Receive(ctx, Run{EndpointID: "ep", ConversationID: "c", SessionID: s.ID, RunID: "r", AttemptID: "a"})
	w.Complete(ctx, "r", "a", Finish{Status: "succeeded"})
	l.PrepareReport(ctx, "ep", "r", "a", json.RawMessage(`{"type":"event","kind":"done"}`))
	l.ReleaseSession(s, 0, true, "r", "a")
	l, _ = Open(root)
	if e = l.Writer().Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if len(l.Pending("ep")) != 1 {
		t.Fatal("pending result lost")
	}
	if _, e = l.Reclaim(ctx, 1); e == nil {
		t.Fatal("unconfirmed result reclaimed")
	}
	if e = l.Confirm(ctx, "ep", "r", "a"); e != nil {
		t.Fatal(e)
	}
	if len(l.Pending("ep")) != 0 {
		t.Fatal("confirmed result remains queued")
	}
	if _, e = l.Reclaim(ctx, 1); e != nil {
		t.Fatal(e)
	}
}

func TestProcessLockProtectsSingleWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".client.lock")
	release, e := Lock(path)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Lock(path); e == nil {
		second()
		t.Fatal("second writer acquired lock")
	}
	release()
	next, e := Lock(path)
	if e != nil {
		t.Fatal(e)
	}
	next()
}

func TestBusinessFilesAreNotExecutionMetadata(t *testing.T) {
	l, _ := Open(t.TempDir())
	ctx := context.Background()
	s, cwd, e := l.AcquireSession(ctx, "ep", "c", "a", "custom", "v")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(cwd, "run.json"), []byte("business contents, not JSON"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = l.Writer().Reconcile(ctx); e != nil {
		t.Fatal("recovery parsed business file", e)
	}
	l.ReleaseSession(s, 0, true)
	if _, e = l.Reclaim(ctx, 1); e != nil {
		t.Fatal("cleanup parsed business file", e)
	}
}

func TestStalePendingReportCanBeReclaimed(t *testing.T) {
	ctx := context.Background()
	l, _ := Open(t.TempDir())
	s, _, _ := l.AcquireSession(ctx, "ep", "c", "a", "custom", "v")
	w := l.ForEndpoint("ep")
	w.Receive(ctx, Run{EndpointID: "ep", ConversationID: "c", SessionID: s.ID, RunID: "r", AttemptID: "a"})
	w.Complete(ctx, "r", "a", Finish{Status: "succeeded"})
	l.PrepareReport(ctx, "ep", "r", "a", json.RawMessage(`{"kind":"done"}`))
	l.ReleaseSession(s, 1, true, "r", "a")
	if e := l.RejectReport(ctx, "ep", "r", "a", "lease expired"); e != nil {
		t.Fatal(e)
	}
	if len(l.Pending("ep")) != 0 {
		t.Fatal("stale result still queued")
	}
	if _, e := l.Reclaim(ctx, 1); e != nil {
		t.Fatal("stale result permanently protected directory", e)
	}
}
