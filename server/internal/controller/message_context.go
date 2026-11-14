package controller

import (
	"net/http"
	"strings"
)

func (s *Server) handleMessageContext(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(requestToken(r), "dk_") {
		writeErr(w, 401, "device credential required")
		return
	}
	if r.Header.Get(actAsHeader) != "" {
		writeErr(w, 403, "device cannot impersonate users")
		return
	}
	d, err := s.service.DeviceService.DeviceByKey(r.Context(), requestToken(r))
	if err != nil {
		writeErr(w, 401, "invalid device credential")
		return
	}
	result, err := s.service.ConversationService.CompleteMessages(r.Context(), d.ID, r.PathValue("id"), r.PathValue("message_id"), r.PathValue("agent_id"), r.URL.Query().Get("session_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
