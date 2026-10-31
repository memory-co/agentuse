package session

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestLocalLedgerReadOnlyUI(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	home := t.TempDir()
	cfg := filepath.Join(home, "daemon.json")
	c := agentuse.New(server.URL, server.AdminToken)
	user := "ledger-user"
	d, err := c.CreateDaemon(ctx, user, "local ledger machine")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateHarness(ctx, user, agentuse.CreateHarnessRequest{Name: "fake"}); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, cfg, map[string]any{"harnesses": map[string]any{"claude": map[string]any{"disabled": true}, "codex": map[string]any{"disabled": true}, "fake": map[string]any{"command": fake}}})
	env := e2eutil.Env(home, "AGENTUSE_UI_ADDR=127.0.0.1:0")
	cmd := exec.Command(cli, "register", "-server", server.URL, "-token", d.SetupToken, "-config", cfg)
	cmd.Env = env
	e2eutil.MustRun(t, cmd)
	cmd = exec.Command(cli, "daemon", "-config", cfg)
	cmd.Env = env
	process := e2eutil.Start(t, cmd)
	var base, token string
	e2eutil.WaitFor(t, 10*time.Second, "local UI and daemon", func() bool {
		ds, e := c.ListDaemons(ctx, user)
		if e != nil || len(ds) != 1 || ds[0].Status != "online" {
			return false
		}
		b, e := os.ReadFile(filepath.Join(home, ".agentuse", "ui.addr"))
		if e != nil {
			return false
		}
		base = strings.TrimSpace(string(b))
		b, e = os.ReadFile(filepath.Join(home, ".agentuse", "ui.token"))
		token = strings.TrimSpace(string(b))
		return e == nil
	})
	if _, err = c.SetDaemonHarness(ctx, user, d.ID, "fake", true); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateAgent(ctx, user, agentuse.CreateAgentRequest{ID: "ledger-agent", Name: "Ledger agent", DaemonID: d.ID, Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{Prompt: "ledger", TargetAgentIDs: []string{"ledger-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.WaitMessageRuns(ctx, user, created.Session.ID, created.Message.ID, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var rows struct {
		Runs []struct {
			RunID  string          `json:"run_id"`
			Status string          `json:"status"`
			Sender agentuse.Sender `json:"sender"`
			Usage  *agentuse.Usage `json:"usage"`
		}
		Total int `json:"total"`
	}
	e2eutil.WaitFor(t, 5*time.Second, "local usage recorded", func() bool {
		req, _ := http.NewRequest("GET", base+"/api/runs", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			return false
		}
		defer r.Body.Close()
		if json.NewDecoder(r.Body).Decode(&rows) != nil {
			return false
		}
		return len(rows.Runs) == 1 && rows.Runs[0].Status == "done"
	})
	row := rows.Runs[0]
	if row.RunID != created.Runs[0].ID || row.Sender.ID != user || row.Usage == nil || row.Usage.InputTokens != 1234 || row.Usage.OutputTokens != 56 {
		t.Fatalf("ledger metadata %+v", row)
	}
	for _, method := range []string{"POST", "DELETE", "PATCH"} {
		req, _ := http.NewRequest(method, base+"/api/runs", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 405 {
			t.Fatalf("write %s returned %d", method, r.StatusCode)
		}
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	r, err := browser.Get(base + "/?token=" + token)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(body), "id=\"root\"") {
		t.Fatalf("UI login %d %s", r.StatusCode, body)
	}
	r, err = browser.Get(base + "/api/stats?group=agent")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(body), "1234") {
		t.Fatalf("stats %d %s", r.StatusCode, body)
	}
	cmd = exec.Command(cli, "ui", "-config", cfg)
	cmd.Env = env
	if link := e2eutil.MustRun(t, cmd); !strings.Contains(link, base) || !strings.Contains(link, "token=") {
		t.Fatal("ui link", link)
	}
	process.Stop()
	// Ledger data survives even after the execution service stops.
	if st, e := os.Stat(filepath.Join(home, ".agentuse", "ledger.db")); e != nil || st.Size() == 0 {
		t.Fatal("ledger missing", e)
	}
}
