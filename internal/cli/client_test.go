package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientConfigPrecedence(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "server-cli.json")
	str := func(s string) *string { return &s }
	h := &flagsHolder{server: str(""), token: str(""), config: str(cfg)}
	t.Setenv("AGENTUSE_SERVER", "")
	t.Setenv("AGENTUSE_TOKEN", "")

	if _, err := h.client(); err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("nothing configured: %v", err)
	}
	saveClientConfig(cfg, clientConfig{Server: "http://file/", Token: "ut_file"})
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o600 {
		t.Fatalf("令牌文件权限应为 0600: %v", st.Mode())
	}
	if c, _ := h.client(); c.server != "http://file" || c.token != "ut_file" {
		t.Fatalf("file: %+v", c)
	}
	t.Setenv("AGENTUSE_SERVER", "http://env")
	if c, _ := h.client(); c.server != "http://env" || c.token != "ut_file" {
		t.Fatalf("env over file: %+v", c)
	}
	*h.token = "ut_flag"
	if c, _ := h.client(); c.server != "http://env" || c.token != "ut_flag" {
		t.Fatalf("flag over env: %+v", c)
	}
	os.WriteFile(cfg, []byte("{"), 0o600)
	if _, err := h.client(); err == nil || !strings.Contains(err.Error(), cfg) {
		t.Fatalf("bad config: %v", err)
	}
}

func TestAPIClientErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ut_ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.EscapedPath() { // user id 要转义
		case "/api/users":
			w.WriteHeader(http.StatusForbidden)
		case "/api/users/x%2Fy/token":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"服务说的错误"}`))
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	c := &apiClient{server: ts.URL, token: "ut_ok"}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{func() error { _, err := (&apiClient{server: ts.URL, token: "bad"}).me(ctx); return err }(), "令牌无效"},
		{func() error { _, err := c.listUsers(ctx); return err }(), "需要管理员令牌"},
		{func() error { _, err := c.resetToken(ctx, "x/y"); return err }(), "服务说的错误"},
		{func() error { _, err := c.me(ctx); return err }(), "418"},
	} {
		if tc.err == nil || !strings.Contains(tc.err.Error(), tc.want) {
			t.Errorf("want %q, got %v", tc.want, tc.err)
		}
	}
}
