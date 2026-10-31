package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/client/internal/store"
)

func TestReadOnlyUIAuthenticationAndHost(t *testing.T) {
	dir := t.TempDir()
	ledger, err := store.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if err = ledger.Writer().Receive(context.Background(), store.Run{RunID: "r1", AttemptID: "a1", Summary: "visible summary"}); err != nil {
		t.Fatal(err)
	}
	token, err := Token(filepath.Join(dir, "ui.token"))
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := Token(filepath.Join(dir, "ui.token")); again != token {
		t.Fatal("token changed across restart")
	}
	if st, _ := os.Stat(filepath.Join(dir, "ui.token")); st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	ui, err := New(ledger.Reader(), "127.0.0.1:7801", token)
	if err != nil {
		t.Fatal(err)
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
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"} {
		if w := call(method, "/api/runs", "127.0.0.1:7801", token); w.Code != 405 {
			t.Fatal(method, w.Code)
		}
	}
	for _, host := range []string{"evil.example:7801", "localhost.evil:7801", "127.0.0.1:9999"} {
		if w := call("GET", "/api/runs", host, token); w.Code != 403 {
			t.Fatal(host, w.Code)
		}
	}
	if w := call("GET", "/api/runs", "localhost:7801", "bad"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/api/runs", "localhost:7801", token); w.Code != 200 || !strings.Contains(w.Body.String(), "visible summary") {
		t.Fatal(w.Code, w.Body.String())
	}
	login := call("GET", "/?token="+token, "localhost:7801", "")
	if login.Code != 303 || strings.Contains(login.Header().Get("Location"), token) {
		t.Fatal("token not removed from URL")
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie security", cookies)
	}
	request := httptest.NewRequest("GET", "http://localhost:7801/api/runs", nil)
	request.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal("cookie authentication", w.Code)
	}
	if _, err = Listen("0.0.0.0:0"); err == nil {
		t.Fatal("non-loopback bind accepted")
	}
}
