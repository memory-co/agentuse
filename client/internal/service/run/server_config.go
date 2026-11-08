package run

import (
	"os"
	"time"

	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/client/internal/service/setting"
)

// Config is an in-memory endpoint execution context, loaded from YAML files.
type Config struct {
	Harness    *harness.Service `json:"-"`
	EndpointID string           `json:"-"`
	Enabled    bool             `json:"-"`
	Settings   setting.Settings `json:"-"`
	Slots      chan struct{}    `json:"-"`
	Sessions   *session.Service `json:"-"`
	RunService *Service         `json:"-"`
	UIAddr     *string          `json:"ui_addr,omitempty"`
	ConfigPath string           `json:"-"`
	Server     string           `json:"server"`
	DeviceID   string           `json:"device_id"`
	Key        string           `json:"key"`
	// Harnesses provides in-memory harnessctl executable overrides for embedding/tests.
	// Normal installations load manifest packages from the local harness directory.
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
