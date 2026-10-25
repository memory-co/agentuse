// 外部测试包：只用导出的 API,和接入方写法一样。
package agentuse_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// recorded 是服务端收到的一个请求。
type recorded struct {
	Method, Path, Auth, User, ContentType, Body string
}

func server(t *testing.T, handler func(w http.ResponseWriter, r recorded)) (*agentuse.Client, *[]recorded) {
	t.Helper()
	var reqs []recorded
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec := recorded{r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"), r.Header.Get(agentuse.UserHeader), r.Header.Get("Content-Type"), string(b)}
		reqs = append(reqs, rec)
		handler(w, rec)
	}))
	t.Cleanup(ts.Close)
	return agentuse.New(ts.URL+"/", "ut_admin"), &reqs
}

func TestRequests(t *testing.T) {
	c, reqs := server(t, func(w http.ResponseWriter, r recorded) {
		switch {
		case r.Method == "DELETE":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.Path, "/api/sessions/"):
			w.Write([]byte(`{"session":{"id":"ss_1","status":"idle"},"messages":[],"events":[]}`))
		default:
			w.Write([]byte(`{"id":"x"}`))
		}
	})
	ctx := context.Background()
	name := "新名字"
	c.Me(ctx, "")
	c.Me(ctx, "u 1")
	c.CreateSession(ctx, "u1", agentuse.CreateSessionRequest{AgentID: "coder", Prompt: "hi"})
	c.UpdateAgent(ctx, "u1", "a/b", agentuse.UpdateAgentRequest{Name: &name})
	c.SetDaemonHarness(ctx, "u1", "dm_1", "claude", true)
	if err := c.DeleteSession(ctx, "u1", "ss_1"); err != nil {
		t.Fatal(err)
	}
	if d, err := c.GetSession(ctx, "u1", "ss_1"); err != nil || d.Session.Status != "idle" {
		t.Fatalf("get: %v %+v", err, d)
	}

	want := []recorded{
		{"GET", "/api/me", "Bearer ut_admin", "", "", ""},
		{"GET", "/api/me", "Bearer ut_admin", "u 1", "", ""},
		{"POST", "/api/sessions", "Bearer ut_admin", "u1", "application/json", `{"agent_id":"coder","prompt":"hi"}`},
		{"PATCH", "/api/agents/a%2Fb", "Bearer ut_admin", "u1", "application/json", `{"name":"新名字"}`},
		{"PUT", "/api/daemons/dm_1/harnesses/claude", "Bearer ut_admin", "u1", "application/json", `{"enabled":true}`},
		{"DELETE", "/api/sessions/ss_1", "Bearer ut_admin", "u1", "", ""},
		{"GET", "/api/sessions/ss_1", "Bearer ut_admin", "u1", "", ""},
	}
	if len(*reqs) != len(want) {
		t.Fatalf("requests: %+v", *reqs)
	}
	for i, w := range want {
		if (*reqs)[i] != w {
			t.Errorf("[%d]\ngot  %+v\nwant %+v", i, (*reqs)[i], w)
		}
	}
}

func TestErrors(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, r recorded) {
		switch r.Path {
		case "/api/sessions/busy/messages":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"会话还在等 harness 回复"}`))
		case "/api/users":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("plain text\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	_, err := c.SendMessage(ctx, "u1", "busy", agentuse.MessageRequest{Prompt: "x"})
	var e *agentuse.Error
	if !errors.As(err, &e) || e.Status != 409 || e.Message != "会话还在等 harness 回复" || !agentuse.IsStatus(err, 409) || agentuse.IsStatus(err, 404) {
		t.Fatalf("json error: %v", err)
	}
	if _, err := c.ListUsers(ctx); !agentuse.IsStatus(err, 403) || err.Error() != "agentuse: 403 plain text" {
		t.Fatalf("text error: %v", err)
	}
	if _, err := c.GetAgent(ctx, "u1", "x"); err == nil || err.Error() != "agentuse: 404 Not Found" {
		t.Fatalf("empty error: %v", err)
	}
	if agentuse.IsStatus(errors.New("x"), 404) {
		t.Fatal("IsStatus on other errors")
	}
	if _, err := agentuse.New("http://127.0.0.1:1", "t").Me(ctx, ""); err == nil || agentuse.IsStatus(err, 0) {
		t.Fatalf("network error: %v", err)
	}
}

func TestWaitSession(t *testing.T) {
	var n atomic.Int32
	c, _ := server(t, func(w http.ResponseWriter, r recorded) {
		status := "running"
		if n.Add(1) >= 3 {
			status = "idle"
		}
		json.NewEncoder(w).Encode(map[string]any{"session": map[string]string{"id": "ss_1", "status": status}})
	})
	d, err := c.WaitSession(context.Background(), "u1", "ss_1", time.Millisecond)
	if err != nil || d.Session.Status != "idle" || n.Load() != 3 {
		t.Fatalf("%v %+v %d", err, d, n.Load())
	}

	// 一直不 idle:ctx 到期就返回
	n.Store(-1000)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = c.WaitSession(ctx, "u1", "ss_1", 10*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
}

func TestCustomHTTPClient(t *testing.T) {
	var used atomic.Bool
	c, _ := server(t, func(w http.ResponseWriter, r recorded) { w.Write([]byte(`[]`)) })
	c.HTTPClient = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		used.Store(true)
		return http.DefaultTransport.RoundTrip(r)
	})}
	if _, err := c.ListAgents(context.Background(), "u1"); err != nil || !used.Load() {
		t.Fatalf("%v %v", err, used.Load())
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
