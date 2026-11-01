package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// ServiceStatus 看 setup.sh 装的后台服务是否在跑。
func ServiceStatus() string {
	if runtime.GOOS == "darwin" {
		if exec.Command("launchctl", "list", "dev.agentuse.daemon").Run() == nil {
			return "launchd: 已加载"
		}
	} else if out, err := exec.Command("systemctl", "--user", "is-active", "agentuse-daemon").Output(); err == nil || len(out) > 0 {
		if st := strings.TrimSpace(string(out)); st != "" && st != "inactive" && !strings.Contains(st, "unknown") {
			return "systemd --user: " + st
		}
	}
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".agentuse", "daemon.pid")); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			if p, err := os.FindProcess(pid); err == nil && p.Signal(syscall.Signal(0)) == nil {
				return fmt.Sprintf("nohup: 运行中(pid %d)", pid)
			}
		}
	}
	return "未检测到"
}
