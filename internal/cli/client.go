package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// clientConfig 是 agentuse-server login 存的 ~/.agentuse/server-cli.json。
type clientConfig struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func defaultClientConfig() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "server-cli.json")
}

func saveClientConfig(path string, c clientConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

type clientFlagSet struct{ *flag.FlagSet }

type flagsHolder struct{ server, token, config *string }

// client 按 参数 → 环境变量 → login 存的配置 的顺序找服务地址和令牌。
func (h *flagsHolder) client() (*agentuse.Client, error) {
	var file clientConfig
	if b, err := os.ReadFile(*h.config); err == nil {
		if err := json.Unmarshal(b, &file); err != nil {
			return nil, fmt.Errorf("%s: %w", *h.config, err)
		}
	}
	pick := func(flagVal, env, fileVal string) string {
		if flagVal != "" {
			return flagVal
		}
		if v := os.Getenv(env); v != "" {
			return v
		}
		return fileVal
	}
	c := &agentuse.Client{
		BaseURL: strings.TrimRight(pick(*h.server, "AGENTUSE_SERVER", file.Server), "/"),
		Token:   pick(*h.token, "AGENTUSE_TOKEN", file.Token),
	}
	if c.BaseURL == "" || c.Token == "" {
		return nil, errors.New("不知道连哪个服务：先运行 agentuse-server login -server URL -token 管理员令牌,或加 -server / -token")
	}
	return c, nil
}
