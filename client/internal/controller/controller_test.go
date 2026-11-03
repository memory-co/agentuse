package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runservice "github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/client/internal/service/session"
)

func TestScopedFileAPIAndAuthentication(t *testing.T) {
	root := t.TempDir()
	l, _ := openFileServices(root)
	ctx := context.Background()
	s, _, e := l.Sessions.Acquire(ctx, "http---localhost%3A8090", "c", "coder", "codex", "v1")
	if e != nil {
		t.Fatal(e)
	}
	if e = l.AcceptNative(ctx, s, "550e8400-e29b-41d4-a716-446655440000"); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(root, s.EndpointID, "c", "sessions", s.ID, "events.jsonl")
	if e = os.WriteFile(source, []byte(`{"type":"accepted","run_id":"550e8400-e29b-41d4-a716-446655440000","text":"visible summary"}`+"\n"), 0600); e != nil {
		t.Fatal(e)
	}

	// The list feeds the inline conversation; only logs belong in details.
	f, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []map[string]any{
		{"type": "message", "text": "first response", "message_id": "m1"},
		{"type": "accepted", "text": "interruption", "input_id": "i2"},
		{"type": "message", "text": "final response", "message_id": "m2"},
		{"type": "log", "message": "private diagnostic"},
		{"type": "result", "status": "completed", "output": "final response"},
	} {
		event["run_id"] = "550e8400-e29b-41d4-a716-446655440000"
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}

	token, e := Token(filepath.Join(t.TempDir(), "ui.token"))
	if e != nil {
		t.Fatal(e)
	}
	ui, e := New(l.Sessions, l, "127.0.0.1:7801", token)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, host, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7801"+path, nil)
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("Origin", "https://foreign.example")
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS exposed")
		}
		return w
	}
	base := "/api/conversations/http---localhost%253A8090/c/sessions/" + s.ID
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"} {
		if w := call(method, base, "localhost:7801", token); w.Code != 405 {
			t.Fatal(method, w.Code)
		}
	}
	for _, host := range []string{"evil.example:7801", "localhost.evil:7801", "127.0.0.1:9999"} {
		if w := call("GET", base, host, token); w.Code != 403 {
			t.Fatal(host, w.Code)
		}
	}
	if w := call("GET", base, "localhost:7801", "bad"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{base, base + "/runs", base + "/runs/550e8400-e29b-41d4-a716-446655440000", base + "/usage", "/api/conversations?endpoint_id=http---localhost%253A8090"} {
		w := call("GET", path, "localhost:7801", token)
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := call("GET", base+"/runs/550e8400-e29b-41d4-a716-446655440000", "localhost:7801", token); !strings.Contains(w.Body.String(), "visible summary") {
		t.Fatal(w.Body.String())
	}
	list := call("GET", base+"/runs?cursor=", "localhost:7801", token)
	var page runservice.Page
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Runs) != 1 {
		t.Fatal(list.Body.String(), err)
	}
	if page.Runs[0].Output != "final response" || len(page.Runs[0].Events) != 5 {
		t.Fatal("missing inline conversation", list.Body.String())
	}
	for _, text := range []string{"visible summary", "first response", "interruption", "final response"} {
		if !strings.Contains(list.Body.String(), text) {
			t.Fatal("missing inline text", text)
		}
	}
	if strings.Contains(list.Body.String(), "private diagnostic") {
		t.Fatal("log leaked into list")
	}
	detail := call("GET", base+"/runs/550e8400-e29b-41d4-a716-446655440000", "localhost:7801", token)
	if !strings.Contains(detail.Body.String(), "private diagnostic") {
		t.Fatal("detail lost diagnostic")
	}
	for _, path := range []string{"/api/runs", "/api/usage", "/api/stats"} {
		if w := call("GET", path, "localhost:7801", token); w.Code != 404 {
			t.Fatal("global scan endpoint exists", path, w.Code)
		}
	}
	if w := call("GET", "/api/conversations", "localhost:7801", token); w.Code != 400 {
		t.Fatal("unscoped conversations accepted")
	}
	if w := call("GET", base+"/runs?limit=bad", "localhost:7801", token); w.Code != 400 {
		t.Fatal(w.Code)
	}
	// Listing conversations must not open nested state files.
	os.WriteFile(filepath.Join(root, s.EndpointID, "c", "sessions", s.ID, "session.yaml"), []byte("{broken"), 0600)
	if w := call("GET", "/api/conversations?endpoint_id=http---localhost%253A8090", "localhost:7801", token); w.Code != 200 {
		t.Fatal("conversation list read session content", w.Code)
	}
	if w := call("GET", base, "localhost:7801", token); w.Code != 500 {
		t.Fatal("damaged state hidden", w.Code)
	}
	ln, e := Listen("0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	ln.Close()
}
func TestTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.token")
	first, e := Token(path)
	if e != nil {
		t.Fatal(e)
	}
	again, e := Token(path)
	if e != nil || first != again {
		t.Fatal("token changed", e)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}

func TestPaginationCursorIsBoundToParent(t *testing.T) {
	ctx := context.Background()
	l, _ := openFileServices(t.TempDir())
	l.Sessions.Acquire(ctx, "ep", "c", "a", "codex", "v")
	l.Sessions.Acquire(ctx, "ep", "c", "b", "claude", "v")
	token := strings.Repeat("x", 64)
	ui, _ := New(l.Sessions, l, "localhost:7801", token)
	call := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost:7801"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	first := call("/api/conversations/ep/c/sessions?limit=1")
	var page struct {
		Items  []session.Session `json:"items"`
		Cursor string            `json:"next_cursor"`
	}
	if e := json.Unmarshal(first.Body.Bytes(), &page); e != nil || len(page.Items) != 1 || page.Cursor == "" {
		t.Fatal(first.Code, first.Body.String(), e)
	}
	second := call("/api/conversations/ep/c/sessions?limit=1&cursor=" + page.Cursor)
	if second.Code != 200 {
		t.Fatal(second.Code, second.Body.String())
	}
	other := call("/api/conversations/ep/other/sessions?limit=1&cursor=" + page.Cursor)
	if other.Code != 400 {
		t.Fatal("cursor crossed conversation", other.Code)
	}
}

func TestPublicListenerKeepsAPIAuthentication(t *testing.T) {
	files, _ := openFileServices(t.TempDir())
	token := strings.Repeat("x", 64)
	ui, e := New(files.Sessions, files, "0.0.0.0:8091", token)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		host, credential string
		status           int
	}{{"public.example:8091", "", 401}, {"public.example:8091", token, 200}, {"203.0.113.5:8091", token, 200}, {"public.example:9999", token, 403}} {
		r := httptest.NewRequest("GET", "http://"+tc.host+"/api/status", nil)
		r.Header.Set("Authorization", "Bearer "+tc.credential)
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc.host, w.Code, tc.status)
		}
	}
}

func TestBearerLoginPersistsInHTTPOnlyCookie(t *testing.T) {
	files, _ := openFileServices(t.TempDir())
	token := strings.Repeat("x", 64)
	ui, _ := New(files.Sessions, files, "0.0.0.0:8091", token)
	call := func(auth string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://public.example:8091/api/status", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	login := call("Bearer "+token, nil)
	cookies := login.Result().Cookies()
	if login.Code != 200 || len(cookies) != 1 {
		t.Fatal(login.Code, cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 30*24*60*60 || cookie.Path != "/api" || cookie.Domain != "" || cookie.Name != "agentuse_client_8091" {
		t.Fatal("invalid cookie attributes", cookie)
	}
	if w := call("", cookie); w.Code != 200 {
		t.Fatal("refresh lost authentication", w.Code)
	}
	if w := call("Bearer invalid", cookie); w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Fatal("invalid explicit token accepted", w.Code)
	}
	if w := call("", nil); w.Code != 401 {
		t.Fatal("unauthenticated request accepted", w.Code)
	}
	restarted, _ := New(files.Sessions, files, "0.0.0.0:8091", strings.Repeat("y", 64))
	r := httptest.NewRequest("GET", "http://public.example:8091/api/status", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	restarted.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("rotated token accepted stale cookie", w.Code)
	}
}

func TestLocalMutationAuthenticationAndOrigin(t *testing.T) {
	files, _ := openFileServices(t.TempDir())
	token := strings.Repeat("x", 64)
	called := 0
	ui, _ := New(files.Sessions, files, "0.0.0.0:8091", token, PublicData{LocalCreate: func(ctx context.Context, title string) (session.Conversation, error) {
		called++
		return files.Sessions.CreateLocalConversation(ctx, title)
	}})
	call := func(path, origin, content, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://public.example:8091"+path, strings.NewReader(`{"title":"自由输入 / ../ 中文"}`))
		r.Header.Set("Content-Type", content)
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	if w := call("/api/local/conversations", "http://public.example:8091", "application/json", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("/api/local/conversations", "https://evil.example", "application/json", token); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("/api/local/conversations", "http://public.example:8091", "text/plain", token); w.Code != 415 {
		t.Fatal(w.Code)
	}
	if w := call("/api/conversations", "http://public.example:8091", "application/json", token); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if called != 0 {
		t.Fatal("unauthorized local mutation reached service")
	}
	w := call("/api/local/conversations", "http://public.example:8091", "application/json", token)
	if w.Code != 201 || called != 1 {
		t.Fatal(w.Code, w.Body.String(), called)
	}
	var c session.Conversation
	if e := json.Unmarshal(w.Body.Bytes(), &c); e != nil || c.EndpointID != "local" || c.Title != "自由输入 / ../ 中文" {
		t.Fatal(c, e)
	}
}

func openFileServices(root string) (*runservice.Service, error) {
	sessions, err := session.New(root)
	if err != nil {
		return nil, err
	}
	return runservice.New(runservice.Dependencies{Sessions: sessions}), nil
}
