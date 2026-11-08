package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/memory-co/agentuse/server/internal/service"
)

type browserNotice struct {
	Type    string                  `json:"type"`
	Changes []service.BrowserChange `json:"changes,omitempty"`
}

// Browser WebSockets use the existing HttpOnly login cookie. Only the acting
// user ID belongs in the URL; credentials must never be placed there.
func (s *Server) handleBrowserWS(w http.ResponseWriter, r *http.Request) {
	if as := r.URL.Query().Get("as"); as != "" {
		r.Header.Set(actAsHeader, as)
	}
	s.auth(s.serveBrowserWS)(w, r)
}
func (s *Server) serveBrowserWS(w http.ResponseWriter, r *http.Request) {
	token := requestToken(r)
	principal, err := s.service.UserByToken(r.Context(), token)
	if err != nil {
		writeErr(w, 401, "invalid user credential")
		return
	}
	// Accept's default origin check rejects cross-origin browser connections.
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	ctx := ws.CloseRead(r.Context())
	sub, unsubscribe := s.service.BrowserChanges.Subscribe(currentUser(r))
	defer unsubscribe()
	valid := func() bool {
		u, e := s.service.UserByToken(ctx, token)
		return e == nil && u.ID == principal.ID && u.Role == principal.Role
	}
	write := func(notice browserNotice) error {
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return wsjson.Write(sendCtx, ws, notice)
	}
	// Subscribe before ready: the browser refetches its active queries on ready.
	if write(browserNotice{Type: "ready"}) != nil {
		return
	}
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	var timer *time.Timer
	var flush <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Wake:
			if flush == nil {
				timer = time.NewTimer(250 * time.Millisecond)
				flush = timer.C
			}
		case <-flush:
			flush = nil
			if !valid() {
				ws.Close(websocket.StatusPolicyViolation, "authentication expired")
				return
			}
			if changes := sub.Drain(); len(changes) > 0 {
				if write(browserNotice{Type: "changed", Changes: changes}) != nil {
					return
				}
			}
		case <-ping.C:
			if !valid() {
				ws.Close(websocket.StatusPolicyViolation, "authentication expired")
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := ws.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
