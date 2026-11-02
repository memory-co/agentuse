package run

import (
	"context"
	"encoding/json"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/client/internal/service/setting"
	"github.com/memory-co/agentuse/client/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localFixture(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	setting, e := setting.New(filepath.Join(home, ".agentuse", "setting.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	files, _ := store.Open(setting.Get().Session.WorkRoot)
	path := filepath.Join(home, ".agentuse", "endpoints.yaml")
	local := New(Dependencies{Store: files, Settings: setting, Endpoints: &endpoint.Service{Path: path}, Sessions: &session.Service{Files: files, Settings: setting}, Harness: &harness.Service{Root: harness.HarnessRoot(harness.Config{ConfigPath: path})}})
	t.Cleanup(local.Close)
	return local, home
}
func awaitLocal(t *testing.T, s *Service, run store.Run) store.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, e := s.Store.Reader().Detail(context.Background(), "local", run.ConversationID, run.SessionID, run.RunID)
		if e != nil {
			t.Fatal(e)
		}
		if record.FinishedAt != nil {
			return record
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("local execution did not finish")
	return store.Run{}
}
func TestLocalCustomHarnessExecutionAndResume(t *testing.T) {
	local, home := localFixture(t)
	t.Setenv("AGENTUSE_SERVER", "https://must-not-connect.invalid")
	t.Setenv("AGENTUSE_TOKEN", "remote-secret")
	installDebugHarness(t, home, `IFS= read -r input
printf '%s' "$input" > received.json
printf '%s|%s|%s' "$AGENTUSE_SERVER" "$AGENTUSE_TOKEN" "$AGENTUSE_WORK" > environment.txt
printf '{"type":"progress","text":"custom progress"}\n'
printf '{"type":"result","ok":true,"output":"local result","session_id":"native-debug"}\n'
`)
	ctx := context.Background()
	title := "任意字符串 / ../ 演示 🚀"
	conversation, e := local.CreateConversation(ctx, endpoint.LocalID, title)
	if e != nil || conversation.EndpointID != "local" || conversation.Title != title {
		t.Fatal(conversation, e)
	}
	first, e := local.Start(ctx, endpoint.LocalID, conversation.ID, Request{Harness: "debug", Prompt: "hello"})
	if e != nil {
		t.Fatal(e)
	}
	got := awaitLocal(t, local, first)
	if got.Status != "completed" || got.Output != "local result" || len(got.Events) < 2 {
		t.Fatal(got)
	}
	second, e := local.Start(ctx, endpoint.LocalID, conversation.ID, Request{Harness: "debug", Input: json.RawMessage(`{"arbitrary":"JSON input"}`)})
	if e != nil {
		t.Fatal(e)
	}
	got = awaitLocal(t, local, second)
	if got.Status != "completed" || second.SessionID != first.SessionID {
		t.Fatal(got)
	}
	b, e := os.ReadFile(filepath.Join(localWorkspace(local, got), "received.json"))
	if e != nil {
		t.Fatal(e)
	}
	var input struct {
		Endpoint     string `json:"endpoint_id"`
		Conversation string `json:"conversation_id"`
		Resume       string `json:"resume"`
		Instructions string `json:"instructions"`
	}
	if e = json.Unmarshal(b, &input); e != nil {
		t.Fatal(e)
	}
	if input.Endpoint != "local" || input.Conversation != conversation.ID || input.Resume != "" || strings.Contains(input.Instructions, "remote-secret") {
		t.Fatal(input)
	}
	if b, e := os.ReadFile(filepath.Join(localWorkspace(local, got), "environment.txt")); e != nil || string(b) != "||" {
		t.Fatal("local harness inherited platform credentials", string(b), e)
	}
	rows, e := local.Store.Reader().Conversations(ctx, "local")
	if e != nil || len(rows) != 1 || rows[0].Title != title {
		t.Fatal(rows, e)
	}
	if _, e = os.Stat(filepath.Join(home, ".agentuse", "endpoints.yaml")); !os.IsNotExist(e) {
		t.Fatal("local mode persisted an endpoint registration")
	}
}
func TestLocalCancelAndRejectRemoteConversation(t *testing.T) {
	local, home := localFixture(t)
	installDebugHarness(t, home, "IFS= read -r input\n: > ready\n/bin/sleep 30\n")
	ctx := context.Background()
	c, e := local.CreateConversation(ctx, endpoint.LocalID, "cancel test")
	if e != nil {
		t.Fatal(e)
	}
	run, e := local.Start(ctx, endpoint.LocalID, c.ID, Request{Harness: "debug", Prompt: "wait"})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, e = os.Stat(filepath.Join(localWorkspace(local, run), "ready")); e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("custom process did not start", e)
	}
	if _, e = local.Control(ctx, endpoint.LocalID, c.ID, run.SessionID, "cancel", json.RawMessage(`{}`)); e != nil {
		t.Fatal(e)
	}
	got := awaitLocal(t, local, run)
	if got.Status != "cancelled" {
		t.Fatal(got)
	}
	if _, e = local.Start(ctx, endpoint.LocalID, "../remote", Request{Harness: "debug", Prompt: "must fail"}); e == nil {
		t.Fatal("unsafe conversation accepted")
	}
	if _, e = local.Start(ctx, endpoint.LocalID, c.ID, Request{Harness: "not-installed", Prompt: "fail"}); e == nil {
		t.Fatal("uninstalled harness accepted")
	}
}

func TestEndpointModeControlsRunSubmission(t *testing.T) {
	runs, home := localFixture(t)
	installDebugHarness(t, home, "exit 0\n")
	ctx := context.Background()
	remote := endpoint.Endpoint{Name: "server", ServerURL: "https://server.example", Enabled: true, DeviceID: "device", DeviceKey: "key"}
	if e := runs.Endpoints.Save(remote); e != nil {
		t.Fatal(e)
	}
	conversation, e := runs.CreateConversation(ctx, endpoint.LocalID, "debug")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = runs.CreateConversation(ctx, remote.ID(), "must not create"); e == nil {
		t.Fatal("manual platform conversation bypassed server")
	}
	if _, e = runs.Start(ctx, remote.ID(), conversation.ID, Request{Harness: "debug", Prompt: "do not run"}); e == nil {
		t.Fatal("platform execution bypassed endpoint mode")
	}
	if runs.localActiveCount() != 0 {
		t.Fatal("rejected endpoint submission started execution")
	}
}

func localWorkspace(s *Service, r store.Run) string {
	return filepath.Join(s.Store.Root, r.EndpointID, r.ConversationID, "workspace")
}
