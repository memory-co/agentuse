// Package config 读 hub 和 daemon 的配置文件。默认位置在 ~/.agentuse/,见 docs/structs/filesystem.md。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	PublicURL    string            `json:"public_url"`    // 用户电脑访问 hub 的地址,写进接入命令;空 = 按请求推断
	Store        string            `json:"store"`         // sqlite(默认)/ memory
	DB           string            `json:"db"`            // sqlite 文件,默认 <home>/hub.db
	TokenKey     string            `json:"token_key"`     // 签运行令牌的密钥
	AdminKey     string            `json:"admin_key"`     // 管理接口的 Bearer 密钥;空 = 关闭
	LeaseSeconds int               `json:"lease_seconds"` // 运行租约,默认 120
	MaxAttempts  int               `json:"max_attempts"`  // 租约过期后最多重领几次(含第一次),默认 3
	Platforms    []models.Platform `json:"platforms"`
}

// Daemon 是用户电脑上 daemon 的配置(daemon.json)。由 agentuse connect 写,一般不用手改。
type Daemon struct {
	Hubs      []HubConn `json:"hubs"`                // 这台电脑在各个 hub 上的设备身份,每个一条循环
	Worktrees string    `json:"worktrees,omitempty"` // worktree 根目录,默认 <home>/worktrees
	Transport string    `json:"transport,omitempty"` // ws(默认,出站 WebSocket)/ poll(HTTP 轮询)
}

// HubConn 是这台电脑在一个 hub 上的一个设备身份(一个平台的一个主人一个)。
type HubConn struct {
	URL       string `json:"url"` // 如 https://agentuse.shop.example.com
	Platform  string `json:"platform"`
	DeviceID  string `json:"device_id"`
	DeviceKey string `json:"device_key"`
}

// On 交回这台电脑在某个 hub 上的全部设备身份。
func (d *Daemon) On(url string) []HubConn {
	var out []HubConn
	for _, h := range d.Hubs {
		if sameURL(h.URL, url) {
			out = append(out, h)
		}
	}
	return out
}

// Upsert 按设备 id 加入或替换一条接入。
func (d *Daemon) Upsert(c HubConn) {
	for i, h := range d.Hubs {
		if h.DeviceID == c.DeviceID {
			d.Hubs[i] = c
			return
		}
	}
	d.Hubs = append(d.Hubs, c)
}

func sameURL(a, b string) bool { return strings.TrimRight(a, "/") == strings.TrimRight(b, "/") }

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

// LoadDaemon 读 daemon.json。
func LoadDaemon(path string) (Daemon, error) {
	var c Daemon
	if err := readJSON(path, &c); err != nil {
		return c, err
	}
	if len(c.Hubs) == 0 {
		return c, errors.New("config: daemon.json has no hubs; run `agentuse connect` first")
	}
	for _, h := range c.Hubs {
		if h.URL == "" || h.DeviceID == "" || h.DeviceKey == "" {
			return c, errors.New("config: every hub in daemon.json needs url, device_id and device_key")
		}
	}
	return c, nil
}

// SaveDaemon 写 daemon.json(0600,里面有设备密钥)。
func SaveDaemon(path string, c Daemon) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
