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
	srv := New(st, cfg)
	ts := httptest.NewServer(srv)
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

func TestBaseURL(t *testing.T) {
	for _, test := range []struct {
		name, requestURL, publicURL, want string
		headers                           map[string]string
	}{
		{name: "http", requestURL: "http://a.com:8080/x", want: "http://a.com:8080"},
		{name: "tls", requestURL: "https://a.com/x", want: "https://a.com"},
		{name: "proxy chain", requestURL: "http://backend/x", headers: map[string]string{"X-Forwarded-Proto": "https, http", "X-Forwarded-Host": "agentuse.example.com, proxy"}, want: "https://agentuse.example.com"},
		{name: "https page without forwarded protocol", requestURL: "http://a.com/x", headers: map[string]string{"Referer": "https://a.com/"}, want: "https://a.com"},
		{name: "https origin with port", requestURL: "http://a.com:8443/x", headers: map[string]string{"Origin": "https://a.com:8443"}, want: "https://a.com:8443"},
		{name: "final proxy hop is http", requestURL: "http://a.com/x", headers: map[string]string{"X-Forwarded-Proto": "http", "Origin": "https://a.com"}, want: "https://a.com"},
		{name: "forwarded public host", requestURL: "http://backend/x", headers: map[string]string{"X-Forwarded-Host": "agentuse.example.com", "Referer": "https://agentuse.example.com/"}, want: "https://agentuse.example.com"},
		{name: "foreign page", requestURL: "http://a.com/x", headers: map[string]string{"Referer": "https://other.example/"}, want: "http://a.com"},
		{name: "different public port", requestURL: "http://a.com:8080/x", headers: map[string]string{"Origin": "https://a.com:8443"}, want: "http://a.com:8080"},
		{name: "invalid source", requestURL: "http://a.com/x", headers: map[string]string{"Origin": "://", "Referer": "https://other.example@a.com/"}, want: "http://a.com"},
		{name: "http page cannot downgrade tls", requestURL: "https://a.com/x", headers: map[string]string{"Origin": "http://a.com"}, want: "https://a.com"},
		{name: "explicit address wins", requestURL: "http://a.com/x", publicURL: "https://public", headers: map[string]string{"Origin": "https://a.com"}, want: "https://public"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &Server{cfg: Config{PublicURL: test.publicURL}}
			r := httptest.NewRequest("GET", test.requestURL, nil)
			for name, value := range test.headers {
				r.Header.Set(name, value)
			}
			if got := s.baseURL(r); got != test.want {
				t.Fatalf("base URL: %q, want %q", got, test.want)
			}
		})
	}
	if New(nil, Config{PublicURL: "https://x/"}).cfg.PublicURL != "https://x" {
		t.Fatal("PublicURL trailing slash")
	}
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
