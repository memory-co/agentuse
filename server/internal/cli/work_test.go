package cli

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
	server "github.com/memory-co/agentuse/server/internal/controller"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestWorkCLIUsesServerOwnedWorkScope(t *testing.T) {
	st, err := service.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	user, token, err := st.CreateUser(ctx, "alice", "Alice", service.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	device, err := st.CreateDaemon(ctx, user.ID, "executor")
	if err != nil {
		t.Fatal(err)
	}
	device, _, err = st.RegisterDaemon(ctx, device.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DaemonConnected(ctx, device, []service.ReportedHarness{{Name: "fake"}}, []string{protocol.ExecutionProtocol})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetDaemonHarnessEnabled(ctx, user, device.ID, "fake", true); err != nil {
		t.Fatal(err)
	}
	agent, err := st.CreateAgent(ctx, user.ID, "coordinator", "Coordinator", device.ID, "fake", "")
	if err != nil {
		t.Fatal(err)
	}
	work, _, err := st.CreateMultiWork(ctx, user, "work", agent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.New(st, server.Config{FilesDir: t.TempDir(), AttachmentsDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)}))
	defer ts.Close()
	config := filepath.Join(t.TempDir(), "server-cli.json")
	if err = saveClientConfig(config, clientConfig{Server: ts.URL, Token: token}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTUSE_SERVER", "")
	t.Setenv("AGENTUSE_TOKEN", "")
	run := func(action string, rest ...string) string {
		t.Helper()
		args := []string{"work", action, "--config", config}
		if action != "agents" {
			args = append(args, "--work", work.ID)
		}
		args = append(args, rest...)
		var code int
		out, stderr := capture(t, func() { code = Main(args) })
		if code != 0 {
			t.Fatal(action, code, stderr)
		}
		return out
	}
	run("agents")
	var created struct {
		Session service.Session `json:"session"`
	}
	if err = json.Unmarshal([]byte(run("new", "--defer-start")), &created); err != nil || created.Session.WorkID != work.ID {
		t.Fatal(created, err)
	}
	run("send", created.Session.ID, "a note")
	run("read", created.Session.ID)
	run("threads")
	run("resolve", created.Session.ID)
	var code int
	capture(t, func() { code = Main([]string{"work", "threads", "--config", config}) })
	if code != 2 {
		t.Fatal("missing explicit work scope accepted", code)
	}
}
