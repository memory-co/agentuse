// Package securewstest supplies a protocol peer for Client transport tests.
package securewstest

import (
	"context"
	"github.com/coder/websocket"
	"github.com/memory-co/agentuse/internal/securews"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type Server struct {
	*httptest.Server
	Identity *securews.Identity
	Revoked  atomic.Bool
	Code     string
}

func New(t testing.TB) *Server {
	t.Helper()
	id, err := securews.NewIdentity(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Identity: id}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("device bearer leaked into WS upgrade")
			w.WriteHeader(401)
			return
		}
		ws, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer ws.CloseNow()
		c, p, e := securews.Accept(r.Context(), ws, id, true)
		if e != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second*5)
		defer cancel()
		var a securews.Authentication
		if e = c.Read(ctx, &a); e != nil {
			return
		}
		if a.Type == "enroll" {
			if a.Token != "st_good" || s.Revoked.Load() {
				_ = c.Write(ctx, securews.AuthResult{Type: "rejected", Error: "invalid installation code"})
				return
			}
			raw, e := id.Issue(a.CSR, "dm_test", 1, time.Now())
			if e != nil {
				t.Error(e)
				return
			}
			_ = c.Write(ctx, securews.AuthResult{Type: "enrolled", DeviceID: "dm_test", Name: "laptop", UserID: "alice", Certificate: raw})
			return
		}
		if _, e = securews.VerifyProof(id.CA, p, a, time.Now()); e != nil || s.Revoked.Load() {
			_ = c.Write(ctx, securews.AuthResult{Type: "rejected", Error: "revoked"})
			return
		}
		_ = c.Write(ctx, securews.AuthResult{Type: "authenticated", DeviceID: "dm_test", Name: "laptop", UserID: "alice"})
		var message any
		_ = c.Read(ctx, &message)
	}))
	t.Cleanup(s.Close)
	s.Code, err = securews.EncodeInvitation(s.URL, id.CA, "st_good")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
