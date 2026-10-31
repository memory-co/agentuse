// 普通会话场景：编好的 agentuse-server + agentuse daemon + 自研的 fakeharness,
// 走一遍 添加设备 → 注册 → 上报 harness → 建 agent → 开会话 → 回复 → 失败 → 断线重连。
// fakeharness 分别扮演自定义 harness 和 harness.yaml 描述的 claude / codex。
package session

import (
	"context"
	"encoding/json"
	"os"
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

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
