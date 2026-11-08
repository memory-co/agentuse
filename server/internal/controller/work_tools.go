package controller

import (
	"context"
	"net/http"
	"strings"

	"github.com/memory-co/agentuse/server/internal/agenttools"
)

type workToolAuthorization struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id,omitempty"`
	AgentID        string `json:"agent_id"`
	RunID          string `json:"run_id"`
}
type workToolCredential struct {
	Token string `json:"token"`
}

func (s *Server) handleWorkTool(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(agenttools.Work)
}
func (s *Server) handleAuthorizeWorkTool(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !strings.HasPrefix(requestToken(r), "dk_") || r.Header.Get(actAsHeader) != "" {
		writeErr(w, http.StatusUnauthorized, "需要执行设备身份")
		return
	}
	device, err := s.service.DaemonByKey(r.Context(), requestToken(r))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "设备密钥无效")
		return
	}
	var input workToolAuthorization
	if err = readMessageJSON(r, &input); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if input.ConversationID == "" || input.AgentID == "" || input.RunID == "" {
		writeErr(w, 400, "需要来源 conversation/agent/run")
		return
	}
	token, err := s.service.AuthorizeWorkTool(r.Context(), device.ID, input.ConversationID, input.MessageID, input.AgentID, input.RunID)
	if err != nil {
		writeErr(w, http.StatusForbidden, "来源不是本设备正在执行的工作协调输入")
		return
	}
	writeJSON(w, 200, workToolCredential{Token: token})
}

// Work management stays in the Server API and supports any authorized user CLI.
func (s *Server) workManagementAuth(h http.HandlerFunc) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		work, err := s.service.GetWork(r.Context(), currentUser(r), r.PathValue("work_id"))
		if err != nil {
			s.storeErr(w, err)
			return
		}
		owner, err := s.service.GetUser(r.Context(), work.UserID)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), userKey{}, owner)
		ctx = context.WithValue(ctx, workKey{}, &work)
		h(w, r.WithContext(ctx))
	})
}
