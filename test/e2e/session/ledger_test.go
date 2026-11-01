package session

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestClientMultiEndpointFilesAndUI(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	home := t.TempDir()
	cfg := filepath.Join(home, ".agentuse", "endpoints.yaml")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	writeAdapterConfig(t, cfg, map[string]any{"harnesses": map[string]any{"claude": map[string]any{"disabled": true}, "codex": map[string]any{"disabled": true}, "fake": map[string]any{"command": fake}}})
	type platform struct {
		c        *agentuse.Client
		device   *agentuse.Daemon
		endpoint string
	}
	platforms := []platform{}
	for i := 0; i < 2; i++ {
		server := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
		c := agentuse.New(server.URL, server.AdminToken)
		d, e := c.CreateDaemon(ctx, "client-user", "client machine")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.CreateHarness(ctx, "client-user", agentuse.CreateHarnessRequest{Name: "fake"}); e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command(cli, "register", "-server", server.URL, "-token", d.SetupToken, "-config", cfg)
		cmd.Env = e2eutil.Env(home)
		e2eutil.MustRun(t, cmd)
		u, _ := url.Parse(server.URL)
		platforms = append(platforms, platform{c, d, "http---" + strings.ReplaceAll(u.Host, ":", "%3A")})
	}
	b, e := os.ReadFile(cfg)
	if e != nil || strings.Contains(string(b), "endpoint_id:") || strings.Contains(string(b), "\n    id:") {
		t.Fatal("derived ID persisted in YAML", string(b), e)
	}
	cmd := exec.Command(cli, "daemon", "-config", cfg)
	cmd.Env = e2eutil.Env(home, "AGENTUSE_UI_ADDR=127.0.0.1:0")
	process := e2eutil.Start(t, cmd)
	var base, token string
	e2eutil.WaitFor(t, 10*time.Second, "both endpoint connections", func() bool {
		for _, p := range platforms {
			ds, e := p.c.ListDaemons(ctx, "client-user")
			if e != nil || len(ds) != 1 || ds[0].Status != "online" {
				return false
			}
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
	get := func(path string, out any) int {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		if out != nil {
			if e = json.NewDecoder(resp.Body).Decode(out); e != nil {
				t.Fatal(path, e)
			}
		}
		return resp.StatusCode
	}
	var eps struct {
		Items []struct {
			ID  string `json:"endpoint_id"`
			Key string `json:"device_key"`
		}
	}
	if status := get("/api/endpoints", &eps); status != 200 || len(eps.Items) != 3 {
		t.Fatal(status, eps)
	}
	for _, e := range eps.Items {
		if e.Key != "" {
			t.Fatal("device key leaked")
		}
	}
	for _, p := range platforms {
		if _, e = p.c.SetDaemonHarness(ctx, "client-user", p.device.ID, "fake", true); e != nil {
			t.Fatal(e)
		}
		if _, e = p.c.CreateAgent(ctx, "client-user", agentuse.CreateAgentRequest{ID: "client-agent", Name: "Client agent", DaemonID: p.device.ID, Harness: "fake"}); e != nil {
			t.Fatal(e)
		}
		created, e := p.c.CreateSession(ctx, "client-user", agentuse.CreateSessionRequest{Prompt: "ledger", TargetAgentIDs: []string{"client-agent"}})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.c.WaitMessageRuns(ctx, "client-user", created.Session.ID, created.Message.ID, 30*time.Millisecond); e != nil {
			t.Fatal(e)
		}
		prefix := "/api/conversations/" + url.PathEscape(p.endpoint) + "/" + created.Session.ID
		var sessions struct {
			Items []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
		}
		e2eutil.WaitFor(t, 5*time.Second, "local session finalized", func() bool {
			return get(prefix+"/sessions", &sessions) == 200 && len(sessions.Items) == 1 && sessions.Items[0].Status == "idle"
		})
		path := prefix + "/sessions/" + sessions.Items[0].ID
		var rows struct {
			Items []struct {
				RunID     string          `json:"run_id"`
				Status    string          `json:"status"`
				Usage     *agentuse.Usage `json:"usage"`
				Confirmed bool            `json:"server_confirmed"`
			}
		}
		if status := get(path+"/runs", &rows); status != 200 || len(rows.Items) != 1 || rows.Items[0].Status != "succeeded" || !rows.Items[0].Confirmed || rows.Items[0].Usage == nil || rows.Items[0].Usage.InputTokens != 1234 {
			t.Fatal(status, rows)
		}
		var usage any
		if status := get(path+"/usage", &usage); status != 200 {
			t.Fatal(status)
		}
		dir := filepath.Join(home, ".agentuse", "tmp", p.endpoint, created.Session.ID)
		if st, e := os.Stat(filepath.Join(dir, "workspace")); e != nil || !st.IsDir() {
			t.Fatal("workspace missing", e)
		}
		runs, e := filepath.Glob(filepath.Join(dir, "sessions", "*", "runs", "*", "*", "run.json"))
		if e != nil || len(runs) != 1 {
			t.Fatal(runs, e)
		}
		// A second run reuses the same local session and workspace.
		next, e := p.c.SendMessage(ctx, "client-user", created.Session.ID, agentuse.MessageRequest{Prompt: "ledger again", TargetAgentIDs: []string{"client-agent"}})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.c.WaitMessageRuns(ctx, "client-user", created.Session.ID, next.Message.ID, 30*time.Millisecond); e != nil {
			t.Fatal(e)
		}
		e2eutil.WaitFor(t, 5*time.Second, "second local run", func() bool { return get(path+"/runs", &rows) == 200 && len(rows.Items) == 2 })
	}
	for _, path := range []string{"/api/runs", "/api/usage", "/api/stats"} {
		if status := get(path, nil); status != 404 {
			t.Fatal("global scan route", path, status)
		}
	}
	resp, e := http.Get(base + "/")
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "id=\"root\"") {
		t.Fatal("frontend missing", resp.StatusCode)
	}
	cmd = exec.Command(cli, "ui", "-config", cfg)
	cmd.Env = e2eutil.Env(home)
	if link := e2eutil.MustRun(t, cmd); !strings.Contains(link, "#token=") {
		t.Fatal("token is not a fragment", link)
	}
	process.Stop()
	if _, e = os.Stat(filepath.Join(home, ".agentuse", "ledger.db")); !os.IsNotExist(e) {
		t.Fatal("client created SQLite storage")
	}
}

func TestClientNativeSessionsShareWorkspaceAndRunConcurrently(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	cli := e2eutil.Binary(t, "agentuse")
	fake := e2eutil.Binary(t, "fakeharness")
	home := t.TempDir()
	cfg := filepath.Join(home, ".agentuse", "endpoints.yaml")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	writeAdapterConfig(t, cfg, map[string]any{"harnesses": map[string]any{"claude": map[string]any{"command": fake}, "codex": map[string]any{"command": fake}}})
	c := agentuse.New(server.URL, server.AdminToken)
	d, e := c.CreateDaemon(ctx, "native-user", "shared workspace")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(cli, "register", "-server", server.URL, "-token", d.SetupToken)
	cmd.Env = e2eutil.Env(home)
	e2eutil.MustRun(t, cmd)
	cmd = exec.Command(cli, "start")
	cmd.Env = e2eutil.Env(home)
	e2eutil.Start(t, cmd)
	waitOnline(t, c, "native-user", d.ID, 2)
	for _, h := range []string{"codex", "claude"} {
		if _, e = c.SetDaemonHarness(ctx, "native-user", d.ID, h, true); e != nil {
			t.Fatal(e)
		}
		if _, e = c.CreateAgent(ctx, "native-user", agentuse.CreateAgentRequest{ID: h, Name: h, DaemonID: d.ID, Harness: h}); e != nil {
			t.Fatal(e)
		}
	}
	first, e := c.CreateSession(ctx, "native-user", agentuse.CreateSessionRequest{Prompt: "wait-steer", TargetAgentIDs: []string{"codex", "claude"}})
	if e != nil {
		t.Fatal(e)
	}
	e2eutil.WaitFor(t, 10*time.Second, "two native runs active for the same message", func() bool {
		detail, e := c.GetSession(ctx, "native-user", first.Session.ID)
		if e != nil || len(detail.Runs) != 2 {
			return false
		}
		return detail.Runs[0].Status == "running" && detail.Runs[1].Status == "running" && len(detail.Events) >= 4
	})
	u, _ := url.Parse(server.URL)
	ep := "http---" + strings.ReplaceAll(u.Host, ":", "%3A")
	dir := filepath.Join(home, ".agentuse", "tmp", ep, first.Session.ID)
	sessions, e := filepath.Glob(filepath.Join(dir, "sessions", "*", "session.yaml"))
	if e != nil || len(sessions) != 2 {
		t.Fatal("missing parallel sessions", sessions, e)
	}
	for _, p := range sessions {
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), "work_root:") {
			t.Fatal("duplicated workspace path in session", string(data))
		}
	}
	if _, e = os.Stat(filepath.Join(dir, "workspace")); e != nil {
		t.Fatal(e)
	}
	extra, e := c.SendMessage(ctx, "native-user", first.Session.ID, agentuse.MessageRequest{Prompt: "parallel-proof", TargetAgentIDs: []string{"codex", "claude"}})
	if e != nil || len(extra.Runs) != 0 {
		t.Fatal("did not steer both active runs", extra, e)
	}
	detail, e := c.WaitMessageRuns(ctx, "native-user", first.Session.ID, extra.Message.ID, 30*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	if len(detail.Runs) != 2 {
		t.Fatal("unexpected fallback run", detail.Runs)
	}
	for _, r := range detail.Runs {
		if r.Status != "succeeded" {
			t.Fatal("parallel run failed", r)
		}
	}
}

func TestClientLocalHarnessWithoutRegistration(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home := t.TempDir()
	cfg := filepath.Join(home, ".agentuse", "endpoints.yaml")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	fake := e2eutil.Binary(t, "fakeharness")
	cli := e2eutil.Binary(t, "agentuse")
	writeAdapterConfig(t, cfg, map[string]any{"harnesses": map[string]any{"fake": map[string]any{"command": fake}, "claude": map[string]any{"disabled": true}, "codex": map[string]any{"disabled": true}}})
	cmd := exec.Command(cli, "start")
	cmd.Env = e2eutil.Env(home, "AGENTUSE_UI_ADDR=127.0.0.1:0")
	process := e2eutil.Start(t, cmd)
	var base, token string
	e2eutil.WaitFor(t, 5*time.Second, "standalone local client", func() bool {
		b, e := os.ReadFile(filepath.Join(home, ".agentuse", "ui.addr"))
		if e != nil {
			return false
		}
		base = strings.TrimSpace(string(b))
		b, e = os.ReadFile(filepath.Join(home, ".agentuse", "ui.token"))
		token = strings.TrimSpace(string(b))
		return e == nil
	})
	request := func(method, path string, body any, out any) int {
		t.Helper()
		var data io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			data = strings.NewReader(string(b))
		}
		r, _ := http.NewRequestWithContext(ctx, method, base+path, data)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if out != nil {
			if e = json.NewDecoder(response.Body).Decode(out); e != nil {
				t.Fatal(e)
			}
		}
		return response.StatusCode
	}
	var endpoints struct {
		Items []struct {
			ID string `json:"endpoint_id"`
		}
	}
	if status := request("GET", "/api/endpoints", nil, &endpoints); status != 200 || len(endpoints.Items) != 1 || endpoints.Items[0].ID != "local" {
		t.Fatal(status, endpoints)
	}
	var c struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if status := request("POST", "/api/local/conversations", map[string]any{"title": "本地调试 / 自定义字符串"}, &c); status != 201 || c.Title != "本地调试 / 自定义字符串" {
		t.Fatal(status, c)
	}
	var run struct {
		Session string `json:"session_id"`
		Run     string `json:"run_id"`
		Attempt string `json:"attempt_id"`
	}
	if status := request("POST", "/api/local/conversations/"+c.ID+"/runs", map[string]any{"harness": "fake", "prompt": "ledger"}, &run); status != 202 {
		t.Fatal(status, run)
	}
	var result struct {
		Status    string          `json:"status"`
		Output    string          `json:"output"`
		Confirmed bool            `json:"server_confirmed"`
		Usage     *agentuse.Usage `json:"usage"`
	}
	path := "/api/conversations/local/" + c.ID + "/sessions/" + run.Session + "/runs/" + run.Run + "/attempts/" + run.Attempt
	e2eutil.WaitFor(t, 5*time.Second, "standalone custom harness result", func() bool {
		return request("GET", path, nil, &result) == 200 && result.Status == "succeeded" && result.Confirmed
	})
	if result.Output != "ledger result" || result.Usage == nil || result.Usage.InputTokens != 1234 {
		t.Fatal(result)
	}
	if _, e := os.Stat(cfg); !os.IsNotExist(e) {
		t.Fatal("local endpoint was written to endpoints.yaml", e)
	}
	process.Stop()
	cmd = exec.Command(cli, "start")
	cmd.Env = e2eutil.Env(home, "AGENTUSE_UI_ADDR=127.0.0.1:0")
	e2eutil.Start(t, cmd)
	e2eutil.WaitFor(t, 5*time.Second, "restarted local client", func() bool {
		b, e := os.ReadFile(filepath.Join(home, ".agentuse", "ui.addr"))
		if e != nil {
			return false
		}
		base = strings.TrimSpace(string(b))
		return request("GET", path, nil, &result) == 200 && result.Status == "succeeded"
	})
}
