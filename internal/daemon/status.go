package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
)

// AgentInfo 是 agentuse agents 列出的一行。
type AgentInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Found    bool   `json:"found"`
	Disabled bool   `json:"disabled,omitempty"`
}

// ListAgents 按 daemon 的检测逻辑列出本机 agent,包括没装上的内置 agent 和配置里禁用的。
func ListAgents(cfg Config) []AgentInfo {
	found := detectAgents(cfg)
	names := map[string]bool{}
	for n := range builtins {
		names[n] = true
	}
	for n := range cfg.Agents {
		names[n] = true
	}
	out := make([]AgentInfo, 0, len(names))
	for n := range names {
		info := AgentInfo{Name: n, Disabled: cfg.Agents[n].Disabled}
		if la, ok := found[n]; ok {
			info.Path, info.Version, info.Found = la.Path, la.Version, true
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CheckKey 用密钥问服务「我是谁」,验证密钥还有效。不建 WebSocket,不影响正在跑的 daemon。
func CheckKey(ctx context.Context, cfg Config) (protocol.MeResponse, error) {
	var me protocol.MeResponse
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Server, "/")+protocol.MePath, nil)
	if err != nil {
		return me, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return me, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return me, errUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return me, errors.New(resp.Status)
	}
	return me, json.NewDecoder(resp.Body).Decode(&me)
}

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
