package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestServerPublicURLSettings(t *testing.T) {
	e2eutil.Skip(t)
	bin := e2eutil.Binary(t, "agentuse-server")
	home := t.TempDir()
	path := filepath.Join(home, ".agentuse", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("public_url: "+value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start := func(want string, flags, env []string) {
		t.Helper()
		args := []string{"start", "--addr", "127.0.0.1:0", "--db", filepath.Join(home, "server.db")}
		cmd := exec.Command(bin, append(args, flags...)...)
		cmd.Env = e2eutil.Env(home, env...)
		process := e2eutil.Start(t, cmd)
		e2eutil.WaitFor(t, 10*time.Second, "configured public URL", func() bool {
			return strings.Contains(process.Output(), "agentuse 服务已启动: "+want)
		})
		process.Stop()
	}
	write("https://file.example.com")
	start("https://file.example.com", nil, nil)
	start("https://env.example.com", nil, []string{"AGENTUSE_PUBLIC_URL=https://env.example.com"})
	start("https://flag.example.com", []string{"--public-url", "https://flag.example.com"}, []string{"AGENTUSE_PUBLIC_URL=https://env.example.com"})
	start("http://127.0.0.1:", []string{"--public-url", ""}, nil)
	if data, err := os.ReadFile(path); err != nil || string(data) != "public_url: https://file.example.com\n" {
		t.Fatal("temporary overrides must not rewrite settings", err)
	}
	write("https://updated.example.com")
	start("https://updated.example.com", nil, nil)
}
