// 普通会话场景：编好的 agentuse-server + agentuse daemon + 自研的 fakeharness,
// 走一遍 添加设备 → 注册 → 上报 harness → 建 agent → 开会话 → 回复 → 失败 → 断线重连。
// fakeharness 分别扮演自定义 harness 和 harness.yaml 描述的 claude / codex。
package session

import (
	"context"
	"encoding/json"
	"fmt"
	bundled "github.com/memory-co/agentuse/harnessctl"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestMain(m *testing.M) { e2eutil.Main(m) }

// result 是 assistant 消息的内容(daemon 回报的结果)。
type result struct {
	OK        bool   `json:"ok"`
	Output    string `json:"output"`
	Error     string `json:"error"`
	SessionID string `json:"session_id"`
}

func lastResult(t *testing.T, d *agentuse.SessionDetail) result {
	t.Helper()
	m := d.Messages[len(d.Messages)-1]
	if m.Role != "assistant" {
		t.Fatalf("最后一条不是 assistant 消息: %+v", m)
	}
	var r result
	if err := json.Unmarshal(m.Content, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func wait(t *testing.T, c *agentuse.Client, user, id string) *agentuse.SessionDetail {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := c.WaitSession(ctx, user, id, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("等会话 %s: %v", id, err)
	}
	return d
}

func hasProgress(d *agentuse.SessionDetail) bool {
	for _, e := range d.Events {
		if e.Kind == "progress" {
			return true
		}
	}
	return false
}

func hasPlan(d *agentuse.SessionDetail, want string) bool {
	for _, e := range d.Events {
		if e.Kind == "plan" && string(e.Payload) == want {
			return true
		}
	}
	return false
}

func waitOnline(t *testing.T, c *agentuse.Client, user, id string, harnesses int) {
	t.Helper()
	e2eutil.WaitFor(t, 20*time.Second, "设备上线并上报 harness", func() bool {
		ds, err := c.ListDaemons(context.Background(), user)
		return err == nil && len(ds) == 1 && ds[0].ID == id && ds[0].Status == "online" && len(ds[0].Harnesses) == harnesses
	})
}

func waitStatus(t *testing.T, c *agentuse.Client, user, id, status string) {
	t.Helper()
	e2eutil.WaitFor(t, 20*time.Second, "设备状态 "+status, func() bool {
		ds, err := c.ListDaemons(context.Background(), user)
		return err == nil && len(ds) == 1 && ds[0].ID == id && ds[0].Status == status
	})
}

func writeAdapterConfig(t *testing.T, path string, v map[string]any) {
	t.Helper()
	root := filepath.Join(filepath.Dir(path), "harness")
	for name, raw := range v["harnesses"].(map[string]any) {
		opts := raw.(map[string]any)
		dir := filepath.Join(root, name, "local")
		os.MkdirAll(dir, 0700)
		if opts["disabled"] == true {
			os.WriteFile(filepath.Join(dir, "disabled"), []byte("true"), 0600)
			continue
		}
		command := opts["command"].(string)
		native := name == "codex" || name == "claude"
		if native {
			source := name
			bin := name
			if name == "claude" {
				source = "claudecode"
				bin = "claude"
			}
			base := "harness/" + source
			err := fs.WalkDir(bundled.Files, base, func(path string, d fs.DirEntry, e error) error {
				if e != nil {
					return e
				}
				rel, _ := filepath.Rel(base, path)
				target := filepath.Join(dir, rel)
				if d.IsDir() {
					return os.MkdirAll(target, 0700)
				}
				data, e := bundled.Files.ReadFile(path)
				if e != nil {
					return e
				}
				return os.WriteFile(target, data, 0700)
			})
			if err != nil {
				t.Fatal(err)
			}
			mb, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
			var manifest map[string]any
			json.Unmarshal(mb, &manifest)
			manifest["requires"] = map[string]any{"python": ">=3.9"}
			mb, _ = json.Marshal(manifest)
			os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0600)
			bins := filepath.Join(dir, "bin")
			os.MkdirAll(bins, 0700)
			os.WriteFile(filepath.Join(bins, bin), []byte(fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", command)), 0700)
			os.Rename(filepath.Join(dir, "harnessctl"), filepath.Join(dir, "entry"))
			os.WriteFile(filepath.Join(dir, "harnessctl"), []byte(fmt.Sprintf("#!/bin/sh\nexport PATH=%q:$PATH\nexec %q \"$@\"\n", bins, filepath.Join(dir, "entry"))), 0700)
		} else {
			script, _ := filepath.Abs("../../../harnessctl/tests/fake_adapter.py")
			os.WriteFile(filepath.Join(dir, "harnessctl"), []byte(fmt.Sprintf("#!/bin/sh\nexec python3 %q %q \"$@\"\n", script, command)), 0700)
			data, _ := json.Marshal(map[string]any{"name": name, "version": "fixture", "protocol": "harnessctl/2", "entrypoint": "harnessctl", "commands": []string{"send", "cancel", "status", "reply"}})
			os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600)
		}
	}
}
