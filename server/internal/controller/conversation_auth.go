package controller

import (
	"context"
	"net/http"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	runservice "github.com/memory-co/agentuse/server/internal/service/run"
)

type sourceKey struct{}

func currentSource(r *http.Request) *runservice.AgentSource {
	v, _ := r.Context().Value(sourceKey{}).(*runservice.AgentSource)
	return v
}

func (s *Server) deviceSource(r *http.Request) (runservice.AgentSource, error) {
	d, err := s.service.DeviceService.DeviceByKey(r.Context(), requestToken(r))
	if err != nil {
		return runservice.AgentSource{}, err
	}
	v, err := s.service.RunService.ExecutionSource(r.Context(), d.ID, r.Header.Get("X-Agentuse-Conversation"), r.Header.Get("X-Agentuse-Agent"), r.Header.Get("X-Agentuse-Session"), r.Header.Get("X-Agentuse-Run"))
	if err != nil {
		return v, err
	}
	u, err := s.service.UserService.GetUser(r.Context(), d.UserID)
	if err != nil {
		return v, err
	}
	u.Role = agentuse.RoleUser
	v.User = u
	if _, err = s.service.ConversationService.GetConversation(r.Context(), u, v.ConversationID); err != nil {
		return v, err
	}
	return v, nil
}

func (s *Server) conversationAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(requestToken(r), "dk_") {
			s.auth(h)(w, r)
			return
		}
		if r.Header.Get(actAsHeader) != "" {
			writeErr(w, 403, "device credentials cannot impersonate users")
			return
		}
		// Read-only access to a conversation actually delivered to this device also
		// supports metadata/resource preparation before native acceptance exists.
		if r.Method == "GET" && r.PathValue("id") != "" && r.Header.Get("X-Agentuse-Conversation") == "" {
			d, err := s.service.DeviceService.DeviceByKey(r.Context(), requestToken(r))
			if err != nil {
				writeErr(w, 401, "invalid device key")
				return
			}
			if err = s.service.RunService.DeviceConversationAccess(r.Context(), d.ID, r.PathValue("id")); err != nil {
				s.serviceErr(w, err)
				return
			}
			u, err := s.service.UserService.GetUser(r.Context(), d.UserID)
			if err != nil {
				s.serviceErr(w, err)
				return
			}
			u.Role = agentuse.RoleUser
			h(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
			return
		}
		source, err := s.deviceSource(r)
		if err != nil {
			writeErr(w, 403, "execution source is not yet reported or is outside this device scope")
			return
		}
		ctx := context.WithValue(r.Context(), userKey{}, source.User)
		ctx = context.WithValue(ctx, sourceKey{}, &source)
		h(w, r.WithContext(ctx))
	}
}
