package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/memory-co/agentuse/internal/protocol"
	"net/http"
	"strings"
	"time"
)

// CheckKey 用密钥问服务「我是谁」,验证密钥还有效。不建 WebSocket,不影响正在跑的 daemon。
func CheckKey(ctx context.Context, cfg Endpoint) (protocol.MeResponse, error) {
	var me protocol.MeResponse
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.ServerURL, "/")+protocol.MePath, nil)
	if err != nil {
		return me, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.DeviceKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return me, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return me, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return me, errors.New(resp.Status)
	}
	return me, json.NewDecoder(resp.Body).Decode(&me)
}

var ErrUnauthorized = errors.New("device 密钥无效，请重新注册 endpoint")
