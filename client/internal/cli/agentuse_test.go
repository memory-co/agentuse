package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	daemon "github.com/memory-co/agentuse/client/internal/service"
)

func TestAgentuseCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTUSE_UI_ADDR", "")
	var deleted bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deleted {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/daemon/register":
			var req struct {
				Token string `json:"token"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Token != "st_good" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"daemon_id":"dm_test","name":"laptop","key":"dk_test"}`))
		case "/api/daemon/me":
			w.Write([]byte(`{"daemon_id":"dm_test","name":"laptop","user_id":"alice","status":"offline"}`))
		default:
			w.WriteHeader(401)
		}
	}))
	defer ts.Close()
	d := struct{ ID, SetupToken string }{"dm_test", "st_good"}
	cfg := filepath.Join(t.TempDir(), "daemon.json")
	t.Setenv("PATH", t.TempDir()) // 本机没有 claude / codex
	t.Setenv("TOKEN", "")
	run := func(args ...string) (int, string, string) {
		var code int
		out, errOut := capture(t, func() { code = Main(append(args, "-config", cfg)) })
		return code, out, errOut
	}

	// 没注册
	if code, _, errOut := run("status"); code != 1 || !strings.Contains(errOut, "还没注册") {
		t.Fatalf("status before register: %d %q", code, errOut)
	}
	if code, _, errOut := run("daemon"); code != 1 || !strings.Contains(errOut, "还没注册") {
		t.Fatalf("daemon before register: %d %q", code, errOut)
	}
	if code, _, _ := run("register", "-server", ts.URL); code != 2 {
		t.Fatalf("register without token: %d", code)
	}
	if code, _, errOut := run("register", "-server", ts.URL, "-token", "st_bad"); code != 1 || !strings.Contains(errOut, "注册失败") {
		t.Fatalf("register bad token: %d %q", code, errOut)
	}
	// TOKEN 环境变量(setup.sh 就是这么传的)
	t.Setenv("TOKEN", d.SetupToken)
	if code, out, _ := run("register", "-server", ts.URL); code != 0 || !strings.Contains(out, d.ID) {
		t.Fatalf("register: %d %q", code, out)
	}
	if code, out, _ := run("status"); code != 0 || !strings.Contains(out, "正常") || !strings.Contains(out, d.ID) {
		t.Fatalf("status: %d %q", code, out)
	}

	// harnesses:没装的内置 harness 也列出来
	var hs []daemon.HarnessInfo
	code, out, _ := run("harnesses", "-json")
	if json.Unmarshal([]byte(out), &hs); code != 0 || len(hs) != 2 || hs[0].Found {
		t.Fatalf("harnesses -json: %d %q", code, out)
	}
	if code, out, _ := run("harnesses"); code != 0 || !strings.Contains(out, "未安装") {
		t.Fatalf("harnesses: %d %q", code, out)
	}

	// 页面上删掉后，密钥失效
	deleted = true
	if code, out, _ := run("status"); code != 0 || !strings.Contains(out, "失败") {
		t.Fatalf("status after delete: %d %q", code, out)
	}
	if code, _, errOut := run("daemon"); code != 1 || !strings.Contains(errOut, "密钥无效") {
		t.Fatalf("daemon with deleted key: %d %q", code, errOut)
	}
}
