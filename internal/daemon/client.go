package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/hub"
)

// ErrNoRun 表示队列里没有可领取的运行。
var ErrNoRun = errors.New("daemon: no run")

// Client 是 daemon 到 hub 的连接。
//
// 现在是出站 HTTP 轮询;博客设想的出站 WebSocket(15 秒心跳)是下一步,
// 消息形状已在 docs/apis/daemon.md 定好,换传输不改语义。
type Client struct {
	URL  string
	ID   string
	Key  string
	HTTP *http.Client
}

func NewClient(url, id, key string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), ID: id, Key: key, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Claim 领取一条运行。
func (c *Client) Claim(ctx context.Context) (hub.Claimed, error) {
	var out hub.Claimed
	resp, err := c.do(ctx, "/v1/daemon/claim", nil)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return out, ErrNoRun
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// Complete 交回运行结果。
func (c *Client) Complete(ctx context.Context, runID string, done hub.Completion) error {
	resp, err := c.do(ctx, "/v1/runs/"+runID+"/complete", done)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) do(ctx context.Context, path string, v any) (*http.Response, error) {
	var body io.Reader = http.NoBody
	if v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set(hub.HeaderDaemon, c.ID)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("hub %s: %s: %s", path, resp.Status, bytes.TrimSpace(msg))
	}
	return resp, nil
}
