package hub

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/memory-co/agentuse/internal/store"
)

// PingInterval 是推送连接空闲等待时 hub 发 ping 的间隔。
const PingInterval = 15 * time.Second

// Message 是 daemon 推送连接上的一条消息。
//
//	daemon → hub  {"type":"ready"}                    我空闲了,有运行就推给我
//	hub → daemon  {"type":"run","claimed":{…}}        一条已经替你领取好的运行
//
// 心跳(续租)和交回走 HTTP(/v1/runs/{run}/heartbeat、/complete):它们幂等、可重试,
// 不依赖连接还在。连接只负责「有活马上推」;连接断了,已推出去的运行靠租约过期回收。
type Message struct {
	Type    string   `json:"type"`
	Claimed *Claimed `json:"claimed,omitempty"`
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	id, ok := s.daemonID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 读循环单独一个 goroutine:Ping 需要有人在读才能收到 pong,读出错就是连接断了。
	ready := make(chan struct{}, 1)
	go func() {
		defer cancel()
		for {
			var m Message
			if err := wsjson.Read(ctx, c, &m); err != nil {
				return
			}
			if m.Type == "ready" {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ready:
		}
		claimed, err := s.waitForRun(ctx, c, id)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("daemon %s: %v", id, err)
			}
			return
		}
		if err := wsjson.Write(ctx, c, Message{Type: "run", Claimed: &claimed}); err != nil {
			log.Printf("daemon %s: push %s: %v (lease will expire)", id, claimed.Run.ID, err)
			return
		}
	}
}

// waitForRun 一直等到替 daemon 领到一条运行;空闲期间每 PingInterval 发一次 ping。
func (s *Server) waitForRun(ctx context.Context, c *websocket.Conn, daemonID string) (Claimed, error) {
	t := time.NewTicker(PingInterval)
	defer t.Stop()
	for {
		changed := s.hub.Changed() // 先取通道再领,避免错过两者之间的变动
		claimed, err := s.hub.Claim(ctx, daemonID)
		if err == nil {
			return claimed, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return Claimed{}, err
		}
		select {
		case <-ctx.Done():
			return Claimed{}, ctx.Err()
		case <-changed:
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, PingInterval)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return Claimed{}, err
			}
		}
	}
}
