package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/memory-co/agentuse/client/internal/store"
)

func localFixture(t *testing.T) (*LocalService, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	setting, e := NewSettingService(filepath.Join(home, ".agentuse", "setting.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	files, _ := store.Open(setting.Get().Session.WorkRoot)
	local := NewLocalService(files, &SessionService{Files: files, Settings: setting}, filepath.Join(home, ".agentuse", "endpoints.yaml"), make(chan struct{}, 2))
	t.Cleanup(local.Close)
	return local, home
}
func installDebugHarness(t *testing.T, home, script string) {
	t.Helper()
	command := filepath.Join(home, "debug-harness")
	if e := os.WriteFile(command, []byte("#!/bin/sh\n"+script), 0700); e != nil {
		t.Fatal(e)
	}
	if e := store.WriteYAML(filepath.Join(home, ".agentuse", "harness", "debug", "local", "harness.yaml"), map[string]any{"name": "debug", "custom": true, "command": command}); e != nil {
		t.Fatal(e)
	}
}
func awaitLocal(t *testing.T, s *LocalService, run store.Run) store.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, e := s.Files.Reader().Detail(context.Background(), "local", run.ConversationID, run.SessionID, run.RunID, run.AttemptID)
		if e != nil {
			t.Fatal(e)
		}
		if record.FinishedAt != nil && record.ServerConfirmed {
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
	conversation, e := local.CreateConversation(ctx, title)
	if e != nil || conversation.EndpointID != "local" || conversation.Title != title {
		t.Fatal(conversation, e)
	}
	first, e := local.Start(ctx, conversation.ID, LocalRunRequest{Harness: "debug", Prompt: "hello"})
	if e != nil {
		t.Fatal(e)
	}
	got := awaitLocal(t, local, first)
	if got.Status != "succeeded" || got.Output != "local result" || len(got.Events) < 2 {
		t.Fatal(got)
	}
	second, e := local.Start(ctx, conversation.ID, LocalRunRequest{Harness: "debug", Input: json.RawMessage(`{"arbitrary":"JSON input"}`)})
	if e != nil {
		t.Fatal(e)
	}
	got = awaitLocal(t, local, second)
	if got.Status != "succeeded" || second.SessionID != first.SessionID {
		t.Fatal(got)
	}
	b, e := os.ReadFile(filepath.Join(got.Directory, "received.json"))
	if e != nil {
		t.Fatal(e)
	}
	var input struct {
		Endpoint     string `json:"endpoint_id"`
		Conversation string `json:"conversation_id"`
		Session      string `json:"session_id"`
		Resume       string `json:"resume"`
		Instructions string `json:"instructions"`
	}
	if e = json.Unmarshal(b, &input); e != nil {
		t.Fatal(e)
	}
	if input.Endpoint != "local" || input.Conversation != conversation.ID || input.Session != first.SessionID || input.Resume != "native-debug" || strings.Contains(input.Instructions, "remote-secret") {
		t.Fatal(input)
	}
	if b, e := os.ReadFile(filepath.Join(got.Directory, "environment.txt")); e != nil || string(b) != "||" {
		t.Fatal("local harness inherited platform credentials", string(b), e)
	}
	rows, e := local.Files.Reader().Conversations(ctx, "local")
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
	c, e := local.CreateConversation(ctx, "cancel test")
	if e != nil {
		t.Fatal(e)
	}
	run, e := local.Start(ctx, c.ID, LocalRunRequest{Harness: "debug", Prompt: "wait"})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, e = os.Stat(filepath.Join(run.Directory, "ready")); e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("custom process did not start", e)
	}
	if e = local.Control(ctx, c.ID, run.SessionID, run.RunID, run.AttemptID, "cancel", json.RawMessage(`{}`)); e != nil {
		t.Fatal(e)
	}
	got := awaitLocal(t, local, run)
	if got.Status != "cancelled" {
		t.Fatal(got)
	}
	if _, e = local.Start(ctx, "../remote", LocalRunRequest{Harness: "debug", Prompt: "must fail"}); e == nil {
		t.Fatal("unsafe conversation accepted")
	}
	if _, e = local.Start(ctx, c.ID, LocalRunRequest{Harness: "not-installed", Prompt: "fail"}); e == nil {
		t.Fatal("uninstalled harness accepted")
	}
}
