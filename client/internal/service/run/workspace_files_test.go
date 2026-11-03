package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/session"
)

func TestServiceFilesHierarchyAndRestart(t *testing.T) {
	root := t.TempDir()
	l, e := openFileServices(root)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s, workspace, e := l.Sessions.Acquire(ctx, "https---one.example", "conversation", "coder", "codex", "v1")
	if e != nil {
		t.Fatal(e)
	}
	w := l.ServerForEndpoint(s.EndpointID)
	r := Delivery{EndpointID: s.EndpointID, ConversationID: s.ConversationID, SessionID: s.ID, RunID: "run", AttemptID: "attempt", Harness: "codex", Summary: strings.Repeat("中", 220) + "SECRET"}
	if e = w.Receive(ctx, r); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = w.Steer(ctx, "run", "attempt", 7); e != nil {
			t.Fatal(e)
		}
	}
	native := filepath.Join(filepath.Dir(workspace), "sessions", s.ID, "events.jsonl")
	body := `{"type":"result","run_id":"native-1","status":"completed","output":"answer","usage":{"input_tokens":100,"output_tokens":20}}` + "\n"
	if e = os.WriteFile(native, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	if e = w.Complete(ctx, "run", "attempt", DeliveryFinish{HarnessRunIDs: []string{"native-1"}, Status: "succeeded"}); e != nil {
		t.Fatal(e)
	}
	if e = l.Confirm(ctx, s.EndpointID, "run", "attempt"); e != nil {
		t.Fatal(e)
	}
	if e = l.Sessions.Release(s, true); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(filepath.Dir(workspace), "sessions", s.ID, ".server", "run.jsonl")
	if st, e := os.Stat(path); e != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, e)
	}
	l, _ = openFileServices(root)
	got, e := l.DeliveryDetail(ctx, s.EndpointID, s.ConversationID, s.ID, "run", "attempt")
	if e != nil || got.Output != "answer" || got.SteerCount != 1 || len([]rune(got.Summary)) != 200 || strings.Contains(got.Summary, "SECRET") {
		t.Fatal(got, e)
	}
	same, cwd, e := l.Sessions.Acquire(ctx, s.EndpointID, s.ConversationID, "coder", "codex", "v1")
	if e != nil || same.ID != s.ID || cwd != workspace {
		t.Fatal(same, cwd, e)
	}
	other, cwd, e := l.Sessions.Acquire(ctx, s.EndpointID, s.ConversationID, "reviewer", "claude", "v1")
	if e != nil || other.ID == s.ID || cwd != workspace {
		t.Fatal(other, cwd, e)
	}
	if _, _, e = l.Sessions.Acquire(ctx, s.EndpointID, s.ConversationID, "coder", "codex", "v1"); e == nil {
		t.Fatal("busy session reused")
	}
	if _, e = l.DeliveryList(ctx, Filter{}); !errors.Is(e, fileutil.ErrInvalid) {
		t.Fatal("unscoped query allowed", e)
	}
	if _, e = l.DeliveryDetail(ctx, s.EndpointID, "../escape", s.ID, "run", "attempt"); !errors.Is(e, fileutil.ErrInvalid) {
		t.Fatal("path traversal", e)
	}
}
func TestEndpointIsolationAndQuota(t *testing.T) {
	l, _ := openFileServices(t.TempDir())
	ctx := context.Background()
	var first session.Session
	for _, ep := range []string{"https---one", "https---two"} {
		s, dir, e := l.Sessions.Acquire(ctx, ep, "same", "agent", "custom", "v1")
		if e != nil {
			t.Fatal(e)
		}
		if first.ID == "" {
			first = s
		}
		os.WriteFile(filepath.Join(dir, "large"), make([]byte, 4096), 0600)
		w := l.ServerForEndpoint(ep)
		if e = w.Receive(ctx, Delivery{EndpointID: ep, ConversationID: "same", SessionID: s.ID, RunID: "same", AttemptID: "same"}); e != nil {
			t.Fatal(e)
		}
		if e = w.Complete(ctx, "same", "same", DeliveryFinish{Status: "succeeded"}); e != nil {
			t.Fatal(e)
		}
		if e = l.Confirm(ctx, ep, "same", "same"); e != nil {
			t.Fatal(e)
		}
		if ep == "https---two" {
			if e = l.Sessions.Release(s, true); e != nil {
				t.Fatal(e)
			}
		}
	}
	_, e := l.Sessions.ReclaimToLimit(ctx, 1)
	if e == nil {
		t.Fatal("quota should block with in-use workspace")
	}
	if _, e = l.Sessions.Session(ctx, "https---one", "same", first.ID); e != nil {
		t.Fatal("active conversation removed", e)
	}
	cs, e := l.Sessions.Conversations(ctx, "https---two")
	if e != nil || len(cs) != 0 {
		t.Fatal("idle conversation not removed", cs, e)
	}
	external := t.TempDir()
	os.WriteFile(filepath.Join(external, "secret"), []byte("safe"), 0600)
	if e = os.Symlink(external, filepath.Join(l.Sessions.Root, "linked")); e != nil {
		t.Fatal(e)
	}
	if _, e = l.Sessions.Conversations(ctx, "linked"); e == nil {
		t.Fatal("symlink root followed")
	}
	if b, _ := os.ReadFile(filepath.Join(external, "secret")); string(b) != "safe" {
		t.Fatal("external data touched")
	}
}
func TestPendingResultSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l, _ := openFileServices(root)
	s, _, e := l.Sessions.Acquire(ctx, "ep", "c", "a", "custom", "v")
	if e != nil {
		t.Fatal(e)
	}
	w := l.ServerForEndpoint("ep")
	w.Receive(ctx, Delivery{EndpointID: "ep", ConversationID: "c", SessionID: s.ID, RunID: "r", AttemptID: "a"})
	w.Complete(ctx, "r", "a", DeliveryFinish{Status: "succeeded"})
	l.PrepareReport(ctx, "ep", "r", "a", json.RawMessage(`{"type":"event","kind":"done"}`))
	l.Sessions.Release(s, true)
	l, _ = openFileServices(root)
	if e = l.ServerWriter().Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if len(l.Pending("ep")) != 1 {
		t.Fatal("pending result lost")
	}
	if _, e = l.Sessions.ReclaimToLimit(ctx, 1); e == nil {
		t.Fatal("unconfirmed result reclaimed")
	}
	if e = l.Confirm(ctx, "ep", "r", "a"); e != nil {
		t.Fatal(e)
	}
	if len(l.Pending("ep")) != 0 {
		t.Fatal("confirmed result remains queued")
	}
	if _, e = l.Sessions.ReclaimToLimit(ctx, 1); e != nil {
		t.Fatal(e)
	}
}

func TestProcessLockProtectsSingleWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".client.lock")
	release, e := fileutil.Lock(path)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := fileutil.Lock(path); e == nil {
		second()
		t.Fatal("second writer acquired lock")
	}
	release()
	next, e := fileutil.Lock(path)
	if e != nil {
		t.Fatal(e)
	}
	next()
}

func TestBusinessFilesAreNotExecutionMetadata(t *testing.T) {
	l, _ := openFileServices(t.TempDir())
	ctx := context.Background()
	s, cwd, e := l.Sessions.Acquire(ctx, "ep", "c", "a", "custom", "v")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(cwd, "run.json"), []byte("business contents, not JSON"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = l.ServerWriter().Reconcile(ctx); e != nil {
		t.Fatal("recovery parsed business file", e)
	}
	l.Sessions.Release(s, true)
	if _, e = l.Sessions.ReclaimToLimit(ctx, 1); e != nil {
		t.Fatal("cleanup parsed business file", e)
	}
}

func TestStalePendingReportCanBeReclaimed(t *testing.T) {
	ctx := context.Background()
	l, _ := openFileServices(t.TempDir())
	s, _, _ := l.Sessions.Acquire(ctx, "ep", "c", "a", "custom", "v")
	w := l.ServerForEndpoint("ep")
	w.Receive(ctx, Delivery{EndpointID: "ep", ConversationID: "c", SessionID: s.ID, RunID: "r", AttemptID: "a"})
	w.Complete(ctx, "r", "a", DeliveryFinish{Status: "succeeded"})
	l.PrepareReport(ctx, "ep", "r", "a", json.RawMessage(`{"kind":"done"}`))
	l.Sessions.Release(s, true)
	if e := l.RejectReport(ctx, "ep", "r", "a", "lease expired"); e != nil {
		t.Fatal(e)
	}
	if len(l.Pending("ep")) != 0 {
		t.Fatal("stale result still queued")
	}
	if _, e := l.Sessions.ReclaimToLimit(ctx, 1); e != nil {
		t.Fatal("stale result permanently protected directory", e)
	}
}

func openFileServices(root string) (*Service, error) {
	sessions, err := session.New(root)
	if err != nil {
		return nil, err
	}
	return New(Dependencies{Sessions: sessions}), nil
}
