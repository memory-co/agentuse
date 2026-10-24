// Package daemon 是用户电脑上的常驻进程：连上服务、领活、调用本地 CLI harness、汇报。
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
)

// Config 是 ~/.agentuse/daemon.json。
type Config struct {
	Server   string `json:"server"`
	DaemonID string `json:"daemon_id"`
	Key      string `json:"key"`
	// Workdir 是没指定 cwd 的会话的工作目录根，每个会话一个子目录。默认 ~/.agentuse/work。
	Workdir string `json:"workdir,omitempty"`
	// Harnesses 配置本机 harness 的启动命令。内置的(claude / codex)不配也会自动检测;
	// 自定义 harness 必须在这里配，名字要和服务上注册的一致。
	Harnesses map[string]HarnessConfig `json:"harnesses,omitempty"`
}

type HarnessConfig struct {
	Command string   `json:"command"`        // 可执行文件，如 claude、/usr/local/bin/my-harness
	Args    []string `json:"args,omitempty"` // 追加参数，如 ["--permission-mode", "acceptEdits"]
	// Disabled 为 true 时不上报这个 harness。
	Disabled bool `json:"disabled,omitempty"`
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "daemon.json")
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func SaveConfig(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Register 用安装令牌向服务注册这台电脑，把拿到的密钥写进配置文件(保留已有的 harnesses 配置)。
func Register(ctx context.Context, server, token, configPath string) (Config, error) {
	server = strings.TrimRight(server, "/")
	host, _ := os.Hostname()
	body, _ := json.Marshal(protocol.RegisterRequest{Token: token, Hostname: host})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+protocol.RegisterPath, bytes.NewReader(body))
	if err != nil {
		return Config{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Config{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return Config{}, errors.New(e.Error)
	}
	var rr protocol.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return Config{}, err
	}
	cfg, err := LoadConfig(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	cfg.Server, cfg.DaemonID, cfg.Key = server, rr.DaemonID, rr.Key
	if err := SaveConfig(configPath, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
