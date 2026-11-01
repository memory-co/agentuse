package run

import (
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/client/internal/service/setting"
	"github.com/memory-co/agentuse/client/internal/store"
	"os"
	"time"
)

// Config is an in-memory endpoint execution context, loaded from YAML files.
type Config struct {
	Harness    *harness.Service `json:"-"`
	EndpointID string           `json:"-"`
	Enabled    bool             `json:"-"`
	Settings   setting.Settings `json:"-"`
	Slots      chan struct{}    `json:"-"`
	Sessions   *session.Service `json:"-"`
	LocalStore *store.Ledger    `json:"-"`
	UIAddr     *string          `json:"ui_addr,omitempty"`
	ConfigPath string           `json:"-"`
	Server     string           `json:"server"`
	DaemonID   string           `json:"daemon_id"`
	Key        string           `json:"key"`
	// Harnesses 配置本机 harness 的启动命令。内置的(claude / codex)不配也会自动检测;
	// 自定义 harness 必须在这里配，名字要和服务上注册的一致。
	Harnesses map[string]harness.HarnessConfig `json:"harnesses,omitempty"`
	// AskTimeout 是等用户回答提问的最长时间，如 "30m";不填默认 30 分钟。
	AskTimeout string `json:"ask_timeout,omitempty"`
}

func (c Config) askTimeout() time.Duration {
	d, err := time.ParseDuration(c.AskTimeout)
	if err != nil || d <= 0 {
		return harness.DefaultAskTimeout
	}
	return d
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

func (c Config) HarnessConfig() harness.Config {
	return harness.Config{ConfigPath: c.ConfigPath, Harnesses: c.Harnesses}
}
