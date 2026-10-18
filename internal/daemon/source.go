package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/memory-co/agentuse/internal/hub"
)

// Source 是 daemon 拿到下一条运行的方式。
type Source interface {
	// Next 阻塞到领到一条运行。出错时调用方退避后重试。
	Next(ctx context.Context) (hub.Claimed, error)
	Close() error
}

// NewSource 按配置选传输:ws(默认,出站 WebSocket,hub 推)或 poll(HTTP 轮询)。
func NewSource(transport string, c *Client, interval time.Duration) (Source, error) {
	switch transport {
	case "", "ws":
		return &WSSource{Client: c}, nil
	case "poll":
		return &PollSource{Client: c, Interval: interval}, nil
	default:
		return nil, fmt.Errorf("daemon: unknown transport %q", transport)
	}
}

// PollSource 每 Interval 问一次 hub。
type PollSource struct {
	Client   *Client
	Interval time.Duration
}

func (p *PollSource) Next(ctx context.Context) (hub.Claimed, error) {
	for {
		c, err := p.Client.Claim(ctx)
		if !errors.Is(err, ErrNoRun) {
			return c, err
		}
		if !sleep(ctx, p.Interval) {
			return hub.Claimed{}, ctx.Err()
		}
	}
}

func (p *PollSource) Close() error { return nil }

// WSSource 维持一条到 /v1/daemon/connect 的出站 WebSocket:空闲时发 ready,等 hub 推一条运行。
// hub 空闲期间每 15 秒 ping 一次,Read 会自动回 pong;连接断了下次 Next 重连。
type WSSource struct {
	Client *Client
	conn   *websocket.Conn
}

func (w *WSSource) dial(ctx context.Context) error {
	u := w.Client.URL + "/v1/daemon/connect"
	if rest, ok := strings.CutPrefix(u, "http"); ok {
		u = "ws" + rest // http → ws,https → wss
	}
	h := http.Header{}
	w.Client.header(h)
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPHeader: h, HTTPClient: w.Client.HTTP})
	if err != nil {
		return err
	}
	conn.SetReadLimit(8 << 20)
	w.conn = conn
	return nil
}

func (w *WSSource) Next(ctx context.Context) (hub.Claimed, error) {
	if w.conn == nil {
		if err := w.dial(ctx); err != nil {
			return hub.Claimed{}, fmt.Errorf("connect: %w", err)
		}
	}
	claimed, err := w.next(ctx)
	if err != nil {
		w.conn.CloseNow()
		w.conn = nil
	}
	return claimed, err
}

func (w *WSSource) next(ctx context.Context) (hub.Claimed, error) {
	if err := wsjson.Write(ctx, w.conn, hub.Message{Type: "ready"}); err != nil {
		return hub.Claimed{}, err
	}
	for {
		var m hub.Message
		if err := wsjson.Read(ctx, w.conn, &m); err != nil {
			return hub.Claimed{}, err
		}
		if m.Type == "run" && m.Claimed != nil {
			return *m.Claimed, nil
		}
	}
}

func (w *WSSource) Close() error {
	if w.conn == nil {
		return nil
	}
	err := w.conn.Close(websocket.StatusNormalClosure, "bye")
	w.conn = nil
	return err
}
