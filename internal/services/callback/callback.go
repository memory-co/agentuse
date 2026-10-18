// Package callback 用 hub 的签名把事件回调给平台。
package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/pkg/signature"
)

// Sender 把一条事件送到平台。
type Sender interface {
	Send(ctx context.Context, p models.Platform, ev models.Event) error
}

// HTTPSender 是默认实现:POST JSON 到 Platform.CallbackURL,用 Platform.Secret 签名。
type HTTPSender struct {
	Client *http.Client
}

func NewHTTPSender() *HTTPSender {
	return &HTTPSender{Client: &http.Client{Timeout: 10 * time.Second}}
}

func (s *HTTPSender) Send(ctx context.Context, p models.Platform, ev models.Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	signature.SetHeaders(req.Header, p.Secret, body, time.Now())
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("callback %s: %s: %s", p.ID, resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}
