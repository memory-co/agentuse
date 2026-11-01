package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestServiceStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // 没有 systemctl / launchctl
	if got := ServiceStatus(); got != "未检测到" {
		t.Fatalf("nothing: %q", got)
	}
	os.MkdirAll(filepath.Join(home, ".agentuse"), 0o700)
	os.WriteFile(filepath.Join(home, ".agentuse", "daemon.pid"), []byte("\n"+strconv.Itoa(os.Getpid())+"\n"), 0o600)
	if got := ServiceStatus(); got != "nohup: 运行中(pid "+strconv.Itoa(os.Getpid())+")" {
		t.Fatalf("pidfile: %q", got)
	}
}
