package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) (Service, Adapter, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("bundled harnessctl adapters support Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, file, _, _ := runtime.Caller(0)
	source := filepath.Join(filepath.Dir(file), "../../../../harnessctl/tests/fake_backend.py")
	script, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0700)
	for _, name := range []string{"codex", "claude"} {
		if e = os.WriteFile(filepath.Join(bin, name), script, 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	service := Service{Root: filepath.Join(home, "harness")}
	adapter, e := service.Resolve(name)
	if e != nil {
		t.Fatal(e)
	}
	workspace := filepath.Join(home, "conversation", "workspace")
	os.MkdirAll(workspace, 0700)
	adapter.EventsPath = EventsPath(workspace, "session-1")
	t.Cleanup(func() { _ = Cancel(adapter, adapter.EventsPath) })
	return service, adapter, workspace
}
func dispatch(prompt string) string { return prompt }

func waitAccepted(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), `"type": "accepted"`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no harnessctl accepted event")
}
func TestHarnessctlExecutionAndEnvironmentRefresh(t *testing.T) {
	for _, name := range []string{"codex", "claude"} {
		t.Run(name, func(t *testing.T) {
			service, adapter, workspace := fixture(t, name)
			for _, id := range []string{"first-attempt", "second-attempt"} {
				adapter.Env = []string{"AGENTUSE_RUN=" + id}
				result := service.DriveSession(context.Background(), adapter, dispatch("environment"), workspace, NewInput(time.Second), nil)
				if !result.OK || result.Output != id || len(result.RunIDs) != 1 {
					t.Fatalf("%s: %+v", id, result)
				}
			}
			data, _ := os.ReadFile(adapter.EventsPath)
			if strings.Count(string(data), `"type": "accepted"`) != 2 {
				t.Fatal(string(data))
			}
		})
	}
}
func TestHarnessctlSteerCompletionAndBusinessRunMapping(t *testing.T) {
	service, adapter, workspace := fixture(t, "codex")
	input := NewInput(time.Second)
	results := make(chan DriveResult, 1)
	var mu sync.Mutex
	confirmed := []int64{}
	go func() {
		results <- service.DriveSession(context.Background(), adapter, dispatch("hold"), workspace, input, func(Event) {})
	}()
	waitAccepted(t, adapter.EventsPath)
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(adapter.EventsPath)
		if strings.Contains(string(data), `"topic": "tool.started"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native turn did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	input.Send("race", func(Acceptance) { mu.Lock(); confirmed = append(confirmed, 7); mu.Unlock() })
	select {
	case result := <-results:
		if !result.OK || result.Output != "race" || len(result.RunIDs) != 2 {
			t.Fatalf("did not wait for queued native interaction: %+v", result)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("bridge did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(confirmed) != 1 || confirmed[0] != 7 {
		t.Fatal(confirmed)
	}
}
func TestHarnessctlQuestionsPermissionsAndCancellation(t *testing.T) {
	for _, name := range []string{"codex", "claude"} {
		for _, prompt := range []string{"ask", "question"} {
			t.Run(name+"/"+prompt, func(t *testing.T) {
				service, adapter, workspace := fixture(t, name)
				input := NewInput(time.Second)
				asked := false
				answered := false
				result := service.DriveSession(context.Background(), adapter, dispatch(prompt), workspace, input, func(event Event) {
					if event.Type == "request" && event.State == "pending" {
						asked = true
						requestID := event.RequestID
						answer := "Go"
						if prompt == "question" {
							answer = "deny"
						}
						body := `{"q1":"Go"}`
						if event.Kind == "permission" {
							body = answer
						}
						input.Reply(requestID, body, nil)
					}
					if event.Type == "request" && event.State == "replied" {
						answered = true
					}
				})
				if !result.OK || !asked || !answered {
					t.Fatal(result, asked, answered)
				}
			})
		}
	}
	service, adapter, workspace := fixture(t, "codex")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan DriveResult, 1)
	go func() {
		result <- service.DriveSession(ctx, adapter, dispatch("hold"), workspace, NewInput(time.Second), nil)
	}()
	waitAccepted(t, adapter.EventsPath)
	cancel()
	select {
	case got := <-result:
		if got.OK || got.CleanupPending {
			t.Fatal(got)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("native cancellation not confirmed")
	}
}
func TestPackageProtocolAndNoNativeSpecification(t *testing.T) {
	service, _, _ := fixture(t, "codex")
	packages, e := service.Packages()
	if e != nil {
		t.Fatal(e)
	}
	for name, adapter := range packages {
		if adapter.Manifest.Protocol != "harnessctl/2" || filepath.Base(adapter.Path) != "harnessctl" {
			t.Fatal(name, adapter)
		}
	}
	if e = SaveCommandAdapters(service.Root, map[string]HarnessConfig{"claude": {Disabled: true}}); e != nil {
		t.Fatal(e)
	}
	if _, e = service.Resolve("claude"); e == nil {
		t.Fatal("disabled adapter resolved")
	}
}

func TestCriticalReaderWaitsForCompleteLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	row := `{"type":"accepted","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"in-1","text":"hello"}`
	os.WriteFile(path, []byte(row[:40]), 0600)
	var offset int64
	items, err := readEvents(path, &offset)
	if err != nil || len(items) != 0 || offset != 0 {
		t.Fatal(items, offset, err)
	}
	os.WriteFile(path, []byte(row+"\n"), 0600)
	items, err = readEvents(path, &offset)
	if err != nil || len(items) != 1 || offset != int64(len(row)+1) {
		t.Fatal(items, offset, err)
	}
	items, err = readEvents(path, &offset)
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
}

func TestRecoveryCancelsBeforeReleasingSession(t *testing.T) {
	service, adapter, _ := fixture(t, "codex")
	cwd := t.TempDir()
	events := filepath.Join(cwd, "events.jsonl")
	var err error
	if _, err = command(context.Background(), adapter, events, "send", "hold", "--cwd", cwd); err != nil {
		t.Fatal(err)
	}
	if err = service.Recover(context.Background(), []RecoverySession{{Harness: adapter.Name, Revision: adapter.Revision, EventsPath: events}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(events)
	if err != nil || !strings.Contains(string(data), `"status": "cancelled"`) {
		t.Fatal(string(data), err)
	}
}

func TestRecoveryRequiresTheOriginalAdapterRevision(t *testing.T) {
	service, adapter, _ := fixture(t, "codex")
	// Installed historical package remains available even if local overrides win selection.
	if err := SaveCommandAdapters(service.Root, map[string]HarnessConfig{"codex": {Command: "/bin/false"}}); err != nil {
		t.Fatal(err)
	}
	got, err := service.resolveRevision("codex", adapter.Revision)
	if err != nil || got.Path != adapter.Path {
		t.Fatal(got, err)
	}
	if _, err = service.resolveRevision("codex", "missing-revision"); err == nil {
		t.Fatal("recovery silently changed adapter")
	}
}
