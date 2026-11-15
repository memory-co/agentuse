package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/memory-co/agentuse/internal/testdb"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/service"
	conversationservice "github.com/memory-co/agentuse/server/internal/service/conversation"
)

// env 是一个跑在 httptest 上的服务，带一个内存库。
type env struct {
	t   *testing.T
	st  *service.Service
	srv *Server
	ts  *httptest.Server
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	driver, dsn := testdb.Config(t)
	db, err := database.OpenConfig(database.Config{Driver: driver, DSN: dsn})
	var st *service.Service
	if err == nil {
		st = service.New(db)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg.Logger = log.New(io.Discard, "", 0)
	ts := httptest.NewUnstartedServer(nil)
	if cfg.PublicURL == "" {
		cfg.PublicURL = "http://" + ts.Listener.Addr().String()
	}
	srv := New(st, cfg)
	ts.Config.Handler = srv
	ts.Start()
	t.Cleanup(ts.Close)
	return &env{t: t, st: st, srv: srv, ts: ts}
}

// user 建一个用户，返回它的令牌。
func (e *env) user(name, role string) (agentuse.User, string) {
	e.t.Helper()
	u, tok, err := e.st.UserService.CreateUser(context.Background(), "", name, role)
	if err != nil {
		e.t.Fatal(err)
	}
	return u, tok
}

// req 发一个请求。body 是 string 时原样发，其它按 JSON 编码;out 不为 nil 时解码响应。
func (e *env) req(tok, method, path string, body, out any, header ...string) *http.Response {
	e.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = bytes.NewBufferString(b)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, r)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultTransport.RoundTrip(req) // 不跟随跳转
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp
}

// code 发请求只看状态码。
func (e *env) code(tok, method, path string, body any) int {
	e.t.Helper()
	return e.req(tok, method, path, body, nil).StatusCode
}

func TestStoreErr(t *testing.T) {
	s := &Server{}
	for err, code := range map[error]int{
		database.ErrNotFound:                                         404,
		fmt.Errorf("wrap: %w", database.ErrNotFound):                 404,
		conversationservice.ErrConversationBusy:                      409,
		errors.New("名字不对"):                                           400,
		fmt.Errorf("x: %w", conversationservice.ErrConversationBusy): 409,
	} {
		w := httptest.NewRecorder()
		s.serviceErr(w, err)
		if w.Code != code {
			t.Errorf("%v → %d, want %d", err, w.Code, code)
		}
	}
}
