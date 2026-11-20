package cli

import (
	"encoding/json"
	harnessservice "github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/internal/securews/securewstest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentuseCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTUSE_UI_ADDR", "")
	peer := securewstest.New(t)
	d := struct{ ID, SetupToken string }{"dm_test", peer.Code}
	cfg := filepath.Join(t.TempDir(), "endpoints.yaml")
	t.Setenv("PATH", t.TempDir()) // 本机没有 claude / codex
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
	if code, _, _ := run("register", "-server", peer.URL); code != 2 {
		t.Fatalf("register without token: %d", code)
	}
	if code, _, errOut := run("register", "-server", peer.URL, "-server-pin", peer.Pin, "-token", "st_bad"); code != 1 || !strings.Contains(errOut, "注册失败") {
		t.Fatalf("register bad token: %d %q", code, errOut)
	}
	if code, out, _ := run("register", "-server", peer.URL, "-server-pin", peer.Pin, "-token", d.SetupToken); code != 0 || !strings.Contains(out, d.ID) {
		t.Fatalf("register: %d %q", code, out)
	}
	if code, out, _ := run("status"); code != 0 || !strings.Contains(out, "正常") || !strings.Contains(out, d.ID) {
		t.Fatalf("status: %d %q", code, out)
	}

	// harnesses:没装的内置 harness 也列出来
	var hs []harnessservice.HarnessInfo
	code, out, _ := run("harnesses", "-json")
	if err := json.Unmarshal([]byte(out), &hs); code != 0 || err != nil || len(hs) != 3 {
		t.Fatalf("harnesses -json: %d %q", code, out)
	}
	for i, name := range []string{"agy", "claude", "codex"} {
		if hs[i].Name != name || hs[i].Found {
			t.Fatalf("unexpected harness: %+v", hs[i])
		}
	}
	if code, out, _ := run("harnesses"); code != 0 || !strings.Contains(out, "未安装") {
		t.Fatalf("harnesses: %d %q", code, out)
	}

	// 页面上删掉后，密钥失效
	peer.Revoked.Store(true)
	if code, out, _ := run("status"); code != 0 || !strings.Contains(out, "失败") {
		t.Fatalf("status after delete: %d %q", code, out)
	}
	if code, _, errOut := run("daemon"); code != 1 || !strings.Contains(errOut, "凭据") && !strings.Contains(errOut, "authentication") {
		t.Fatalf("daemon with deleted key: %d %q", code, errOut)
	}
}
