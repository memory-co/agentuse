package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestServerSelfUpgradeKeepsPIDCredentialsAndHistory(t *testing.T) {
	e2eutil.Skip(t)
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("supported binary platforms")
	}
	const nextVersion = "v9.1.0"
	files := map[string][]byte{}
	ready := make(chan struct{})
	serverAsset := "agentuse-server-" + runtime.GOOS + "-" + runtime.GOARCH
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-ready
		if r.URL.Path == "/latest" {
			assets := []map[string]any{}
			for name, data := range files {
				assets = append(assets, map[string]any{"name": name, "size": len(data)})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": nextVersion, "html_url": "https://github.com/memory-co/agentuse/releases/tag/" + nextVersion, "assets": assets})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/download/"+nextVersion+"/")
		data, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer github.Close()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	build := func(version, path string) {
		t.Helper()
		flags := "-X github.com/memory-co/agentuse/internal/version.Version=" + version + " -X github.com/memory-co/agentuse/server/internal/service/upgrade.githubAPIURL=" + github.URL + "/latest -X github.com/memory-co/agentuse/server/internal/service/upgrade.githubDownloadURL=" + github.URL + "/download"
		cmd := exec.Command("go", "build", "-ldflags", flags, "-o", path, "./server/cmd/agentuse-server")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		e2eutil.MustRun(t, cmd)
	}
	home := t.TempDir()
	installed := filepath.Join(home, "agentuse-server")
	build("v9.0.0", installed)
	next := filepath.Join(home, "next-server")
	build(nextVersion, next)
	files[serverAsset], err = os.ReadFile(next)
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"} {
		files["agentuse-"+platform] = []byte("new-client:" + platform)
	}
	var checks strings.Builder
	for name, data := range files {
		fmt.Fprintf(&checks, "%x  %s\n", sha256.Sum256(data), name)
	}
	files["SHA256SUMS"] = []byte(checks.String())
	close(ready)
	const password = "upgrade-test-password"
	pidfile := filepath.Join(home, "server.pid")
	start := exec.Command(installed, "start", "--addr", "127.0.0.1:0", "--admin-token-stdin", "--pid-file", pidfile)
	start.Env = e2eutil.Env(home)
	start.Stdin = strings.NewReader(password)
	process := e2eutil.Start(t, start)
	var address string
	pattern := regexp.MustCompile(`agentuse 服务已启动: (http://\S+)`)
	e2eutil.WaitFor(t, 10*time.Second, "old Server startup", func() bool {
		match := pattern.FindStringSubmatch(process.Output())
		if match != nil {
			address = match[1]
		}
		return address != ""
	})
	client := agentuse.New(address, password)
	ctx := context.Background()
	conversation, err := client.CreateConversation(ctx, "", agentuse.CreateConversationRequest{Title: "preserved by upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, ".agentuse", "settings.yaml"), []byte("public_url: "+address+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	upgrade := exec.Command(installed, "upgrade", "--server", address, "--token", password, "--wait", "--timeout", "60s", "--json")
	upgrade.Env = e2eutil.Env(home)
	var output, diagnostics bytes.Buffer
	upgrade.Stdout = &output
	upgrade.Stderr = &diagnostics
	if err = upgrade.Run(); err != nil {
		t.Fatalf("CLI upgrade: %v\n%s\n%s", err, output.String(), diagnostics.String())
	}
	var record agentuse.ServerUpgrade
	if err = json.Unmarshal(output.Bytes(), &record); err != nil || record.Status != "succeeded" || record.FromVersion != "v9.0.0" || record.TargetVersion != nextVersion {
		t.Fatal(output.String(), err)
	}
	state, err := client.ServerUpgrades(ctx, "", 20)
	if err != nil || state.CurrentVersion != nextVersion || state.Active != nil || len(state.Upgrades) != 1 {
		t.Fatal(state, err)
	}
	pid, err := os.ReadFile(pidfile)
	if err != nil || strings.TrimSpace(string(pid)) != fmt.Sprint(start.Process.Pid) {
		t.Fatal("PID changed", string(pid), err)
	}
	if got := strings.Count(process.Output(), "已设置管理员登录密码"); got != 1 {
		t.Fatal("one-shot password setup repeated", process.Output())
	}
	if _, err = client.GetConversation(ctx, "", conversation.Conversation.ID); err != nil {
		t.Fatal("conversation or credential was lost", err)
	}
	version := exec.Command(installed, "version")
	if out := e2eutil.MustRun(t, version); strings.TrimSpace(out) != nextVersion {
		t.Fatal(out)
	}
	for name, data := range files {
		if !strings.HasPrefix(name, "agentuse-") || name == serverAsset {
			continue
		}
		actual, e := os.ReadFile(filepath.Join(home, ".agentuse", "dist", name))
		if e != nil || !bytes.Equal(actual, data) {
			t.Fatal("client distribution was not upgraded", name, e)
		}
	}
	second, err := client.StartServerUpgrade(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e2eutil.WaitFor(t, 5*time.Second, "same release does not restart", func() bool {
		item, e := client.GetServerUpgrade(ctx, second.ID)
		return e == nil && item.Status == "unchanged"
	})
	if strings.Count(process.Output(), "agentuse 服务已启动:") != 2 {
		t.Fatal("unexpected restart count", process.Output())
	}
}
