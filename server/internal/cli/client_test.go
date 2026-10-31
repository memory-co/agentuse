package cli

import (
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
	if c, _ := h.client(); c.BaseURL != "http://file" || c.Token != "ut_file" {
		t.Fatalf("file: %+v", c)
	}
	t.Setenv("AGENTUSE_SERVER", "http://env")
	if c, _ := h.client(); c.BaseURL != "http://env" || c.Token != "ut_file" {
		t.Fatalf("env over file: %+v", c)
	}
	*h.token = "ut_flag"
	if c, _ := h.client(); c.BaseURL != "http://env" || c.Token != "ut_flag" {
		t.Fatalf("flag over env: %+v", c)
	}
	os.WriteFile(cfg, []byte("{"), 0o600)
	if _, err := h.client(); err == nil || !strings.Contains(err.Error(), cfg) {
		t.Fatalf("bad config: %v", err)
	}
}
