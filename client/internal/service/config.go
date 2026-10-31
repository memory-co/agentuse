// Package daemon 是用户电脑上的常驻进程：连上服务、领活、调用本地 CLI harness、汇报。
package service

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

	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/protocol"
)

// Config is an in-memory endpoint execution context, loaded from YAML files.
type Config struct {
	EndpointID string          `json:"-"`
	Enabled    bool            `json:"-"`
	Settings   Settings        `json:"-"`
	Slots      chan struct{}   `json:"-"`
	Sessions   *SessionService `json:"-"`
	LocalStore *store.Ledger   `json:"-"`
	UIAddr     *string         `json:"ui_addr,omitempty"`
	ConfigPath string          `json:"-"`
	Server     string          `json:"server"`
	DaemonID   string          `json:"daemon_id"`
	Key        string          `json:"key"`
	// Harnesses 配置本机 harness 的启动命令。内置的(claude / codex)不配也会自动检测;
	// 自定义 harness 必须在这里配，名字要和服务上注册的一致。
	Harnesses map[string]HarnessConfig `json:"harnesses,omitempty"`
	// AskTimeout 是等用户回答提问的最长时间，如 "30m";不填默认 30 分钟。
	AskTimeout string `json:"ask_timeout,omitempty"`
}

func (c Config) askTimeout() time.Duration {
	d, err := time.ParseDuration(c.AskTimeout)
	if err != nil || d <= 0 {
		return defaultAskTimeout
	}
	return d
}

type HarnessConfig struct {
	Command string   `json:"command"`        // 可执行文件，如 claude、/usr/local/bin/my-harness
	Args    []string `json:"args,omitempty"` // 追加参数，如 ["--model", "opus"]
	// Disabled 为 true 时不上报这个 harness。
	Disabled bool `json:"disabled,omitempty"`
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "endpoints.yaml")
}

// Config is an in-memory execution identity. Only endpoints.yaml and setting.yaml
// are persistent configuration; no legacy JSON configuration is read.
func LoadConfigs(path string) ([]Config, error) {
	endpoints, err := (EndpointService{Path: path}).List()
	if err != nil {
		return nil, err
	}
	settings, err := NewSettingService(filepath.Join(filepath.Dir(path), "setting.yaml"))
	if err != nil {
		return nil, err
	}
	var out []Config
	for _, ep := range endpoints {
		c := Config{ConfigPath: path, Server: ep.ServerURL, DaemonID: ep.DeviceID, Key: ep.DeviceKey, EndpointID: ep.ID(), Enabled: ep.Enabled, Settings: settings.Get()}
		out = append(out, c)
	}
	return out, nil
}
func LoadConfig(path string) (Config, error) {
	all, e := LoadConfigs(path)
	if e != nil {
		return Config{}, e
	}
	wanted := os.Getenv("AGENTUSE_SERVER")
	for _, c := range all {
		if wanted != "" && c.Server == strings.TrimRight(wanted, "/") {
			return c, nil
		}
	}
	if wanted != "" {
		return Config{}, fmt.Errorf("server is not registered: %s", wanted)
	}
	for _, c := range all {
		if c.Enabled {
			return c, nil
		}
	}
	if len(all) > 0 {
		return all[0], nil
	}
	return Config{}, fmt.Errorf("no registered endpoints in %s", path)
}
func SaveConfig(path string, c Config) error {
	if err := saveCommandAdapters(filepath.Join(filepath.Dir(path), "harness"), c.Harnesses); err != nil {
		return err
	}
	return (EndpointService{Path: path}).Save(Endpoint{Name: c.Server, ServerURL: c.Server, Enabled: true, DeviceID: c.DaemonID, DeviceKey: c.Key})
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
	cfg := Config{Server: server, DaemonID: rr.DaemonID, Key: rr.Key}
	if err := SaveConfig(configPath, cfg); err != nil {
		return Config{}, err
	}
	return LoadConfigForServer(configPath, server)
}

func (c Config) UIAddress() string {
	if v, ok := os.LookupEnv("AGENTUSE_UI_ADDR"); ok {
		return v
	}
	if c.UIAddr == nil {
		if c.Settings.LocalHTTP.Host != "" {
			return c.Settings.UIAddress()
		}
		return "127.0.0.1:7801"
	}
	return *c.UIAddr
}

func LoadConfigForServer(path, server string) (Config, error) {
	all, e := LoadConfigs(path)
	if e != nil {
		return Config{}, e
	}
	u, _, e := store.NormalizeEndpoint(server)
	if e != nil {
		return Config{}, e
	}
	for _, c := range all {
		if c.Server == u {
			return c, nil
		}
	}
	return Config{}, os.ErrNotExist
}
