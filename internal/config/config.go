// Package config 读 hub 和 daemon 的配置文件。默认位置在 ~/.agentuse/,见 docs/structs/filesystem.md。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/memory-co/agentuse/internal/models"
)

// Home 是 agentuse 的数据目录:$AGENTUSE_HOME,默认 ~/.agentuse。
func Home() string {
	if h := os.Getenv("AGENTUSE_HOME"); h != "" {
		return h
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".agentuse"
	}
	return filepath.Join(dir, ".agentuse")
}

// Hub 是 hub 实例的配置(hub.json)。
type Hub struct {
	Listen       string            `json:"listen"`        // 默认 127.0.0.1:7700
	Store        string            `json:"store"`         // sqlite(默认)/ memory
	DB           string            `json:"db"`            // sqlite 文件,默认 <home>/hub.db
	TokenKey     string            `json:"token_key"`     // 签运行令牌的密钥
	AdminKey     string            `json:"admin_key"`     // 管理接口的 Bearer 密钥;空 = 关闭
	DaemonKeys   map[string]string `json:"daemon_keys"`   // daemon id → 密钥,daemon 领取运行时用
	LeaseSeconds int               `json:"lease_seconds"` // 运行租约,默认 120
	MaxAttempts  int               `json:"max_attempts"`  // 租约过期后最多重领几次(含第一次),默认 3
	Platforms    []models.Platform `json:"platforms"`
}

// Daemon 是用户电脑上 daemon 的配置(daemon.json)。
type Daemon struct {
	HubURL     string               `json:"hub_url"`    // 如 https://hub.alice.dev
	ID         string               `json:"id"`         // 如 alice-mbp
	Key        string               `json:"key"`        // 与 hub.json 的 daemon_keys[id] 一致
	Workspaces map[string]Workspace `json:"workspaces"` // platform id → 现场
	Driver     string               `json:"driver"`     // claude(默认)
	Transport  string               `json:"transport"`  // ws(默认,出站 WebSocket)/ poll(HTTP 轮询)
}

// Workspace 是某个平台的运行落到哪个仓库。
type Workspace struct {
	Repo     string `json:"repo"`     // 如 ~/work/shop-api
	BaseRef  string `json:"base_ref"` // worktree 从哪个分支切,默认 main
	Worktree string `json:"worktree"` // worktree 根目录,默认 <home>/worktrees
}

// LoadHub 读 hub.json 并补默认值。
func LoadHub(path string) (Hub, error) {
	var c Hub
	if err := readJSON(path, &c); err != nil {
		return c, err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:7700"
	}
	if c.Store == "" {
		c.Store = "sqlite"
	}
	if c.DB == "" {
		c.DB = filepath.Join(Home(), "hub.db")
	}
	if c.TokenKey == "" {
		return c, errors.New("config: hub.json needs token_key")
	}
	return c, nil
}

// LoadDaemon 读 daemon.json 并补默认值。
func LoadDaemon(path string) (Daemon, error) {
	var c Daemon
	if err := readJSON(path, &c); err != nil {
		return c, err
	}
	if c.HubURL == "" || c.ID == "" || c.Key == "" {
		return c, errors.New("config: daemon.json needs hub_url, id and key")
	}
	if c.Driver == "" {
		c.Driver = "claude"
	}
	return c, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	return nil
}
