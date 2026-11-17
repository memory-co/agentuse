package controller

import (
	"context"
	"github.com/memory-co/agentuse/internal/access"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	runservice "github.com/memory-co/agentuse/server/internal/service/run"
	"net/http"
	"time"
)

type sourceKey struct{}
type executionKey struct{}

func currentSource(r *http.Request) *runservice.AgentSource {
	v, _ := r.Context().Value(sourceKey{}).(*runservice.AgentSource)
	return v
}
func currentExecution(r *http.Request) *runservice.AccessPrincipal {
	v, _ := r.Context().Value(executionKey{}).(*runservice.AccessPrincipal)
	return v
}
func (s *Server) executionRequest(r *http.Request) (*http.Request, error) {
	if r.Header.Get(actAsHeader) != "" {
		return r, runservice.ErrAccess
	}
	p, err := s.service.RunService.AccessForRequest(r.Context(), requestToken(r))
	if err != nil {
		return r, err
	}
	for header, want := range map[string]string{"X-Agentuse-Conversation": p.Claims.ConversationID, "X-Agentuse-Agent": p.Claims.AgentID, "X-Agentuse-Session": p.SessionID, "X-Agentuse-Run": p.RunID} {
		if got := r.Header.Get(header); got != "" && got != want {
			return r, runservice.ErrAccess
		}
	}
	u, err := s.service.UserService.GetUser(r.Context(), p.UserID)
	if err != nil {
		return r, err
	}
	u.Role = agentuse.RoleUser
	ctx := access.WithLimit(access.WithCredential(r.Context(), requestToken(r)), p.ExpiresAt)
	ctx = context.WithValue(ctx, userKey{}, u)
	ctx = context.WithValue(ctx, executionKey{}, &p)
	if !p.Preparing {
		source := &runservice.AgentSource{User: u, DeviceID: p.DeviceID, ConversationID: p.Claims.ConversationID, AgentID: p.Claims.AgentID, AgentVersionID: p.Claims.AgentVersionID, SessionID: p.SessionID, RunID: p.RunID}
		ctx = context.WithValue(ctx, sourceKey{}, source)
	}
	return r.WithContext(ctx), nil
}
func (s *Server) deviceSource(r *http.Request) (runservice.AgentSource, error) {
	r, err := s.executionRequest(r)
	if err != nil {
		return runservice.AgentSource{}, err
	}
	v := currentSource(r)
	if v == nil {
		return runservice.AgentSource{}, runservice.ErrAccess
	}
	return *v, nil
}
func (s *Server) conversationAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !resourceCredential(r) {
			s.auth(h)(w, r)
			return
		}
		var err error
		r, err = s.executionRequest(r)
		if err != nil {
			writeErr(w, 403, "execution access unavailable")
			return
		}
		p := currentExecution(r)
		if p.Preparing && (r.Method != http.MethodGet || r.PathValue("id") != p.Claims.ConversationID || r.URL.RawQuery != "" || r.URL.Path != "/api/conversations/"+p.Claims.ConversationID) {
			writeErr(w, 403, "preparation access is limited to source conversation metadata")
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), time.Unix(p.ExpiresAt, 0))
		defer cancel()
		h(w, r.WithContext(ctx))
	}
}
