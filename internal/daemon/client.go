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
	"github.com/memory-co/agentuse/internal/models"
)

// ErrNoRun 表示队列里没有可领取的运行。
var ErrNoRun = errors.New("daemon: no run")

// ErrLost 表示这次领取已经不归这台设备了(租约过期被回收,或运行已结束)。
var ErrLost = errors.New("daemon: run no longer held")

// ErrBadCode 表示接入码不对、已用过或已过期。
var ErrBadCode = errors.New("daemon: connect code invalid, used or expired")

// Client 是设备到 hub 的 HTTP 调用:领取(轮询模式)、心跳、交回,都带设备 id 和设备密钥。
// 推送模式下领取走 WebSocket(source.go),心跳和交回仍走这里。
type Client struct {
	URL      string
	DeviceID string
	Key      string
	HTTP     *http.Client
}

func NewClient(url, deviceID, key string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), DeviceID: deviceID, Key: key, HTTP: &http.Client{Timeout: 30 * time.Second}}
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

// Heartbeat 续租。运行已不归自己时返回 ErrLost。
func (c *Client) Heartbeat(ctx context.Context, runID string, attempt int) error {
	resp, err := c.do(ctx, "/v1/runs/"+runID+"/heartbeat", map[string]int{"attempt": attempt})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Complete 交回运行结果。运行已不归自己时返回 ErrLost。
func (c *Client) Complete(ctx context.Context, runID string, done hub.Completion) error {
	resp, err := c.do(ctx, "/v1/runs/"+runID+"/complete", done)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) header(h http.Header) {
	h.Set("Authorization", "Bearer "+c.Key)
	h.Set(hub.HeaderDevice, c.DeviceID)
}

// Info 交回这台设备和绑在上面的 agent。
func (c *Client) Info(ctx context.Context) (DeviceInfo, error) {
	var out DeviceInfo
	resp, err := c.req(ctx, http.MethodGet, "/v1/device", nil)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// DeviceInfo 是 GET /v1/device 的回答。
type DeviceInfo struct {
	Platform string         `json:"platform"`
	Device   hub.DeviceView `json:"device"`
	Agents   []models.Agent `json:"agents"`
}

// Disconnect 把 agent 从这台设备上解绑。
func (c *Client) Disconnect(ctx context.Context, handle string) error {
	resp, err := c.do(ctx, "/v1/device/disconnect", map[string]string{"agent": handle})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) do(ctx context.Context, path string, v any) (*http.Response, error) {
	return c.req(ctx, http.MethodPost, path, v)
}

func (c *Client) req(ctx context.Context, method, path string, v any) (*http.Response, error) {
	var body io.Reader = http.NoBody
	if v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.header(req.Header)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusConflict {
		resp.Body.Close()
		return nil, ErrLost
	}
	if resp.StatusCode/100 != 2 {
		return nil, httpError(path, resp)
	}
	return resp, nil
}

func httpError(path string, resp *http.Response) error {
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("hub %s: %s: %s", path, resp.Status, bytes.TrimSpace(msg))
}

// Connect 拿接入码向 hub 兑换设备身份(POST /v1/connect)。
func Connect(ctx context.Context, hubURL string, req hub.ConnectRequest) (hub.ConnectResponse, error) {
	var out hub.ConnectResponse
	b, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(hubURL, "/")+"/v1/connect", bytes.NewReader(b))
	if err != nil {
		return out, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(r)
	if err != nil {
		return out, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return out, ErrBadCode
	}
	if resp.StatusCode/100 != 2 {
		return out, httpError("/v1/connect", resp)
	}
	defer resp.Body.Close()
	return out, json.NewDecoder(resp.Body).Decode(&out)
}
