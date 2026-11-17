package controller

import "net/http"

func (s *Server) handleMessageContext(w http.ResponseWriter, r *http.Request) {
	if !resourceCredential(r) {
		writeErr(w, 401, "input credential required")
		return
	}
	r, err := s.executionRequest(r)
	if err != nil {
		writeErr(w, 403, "execution access unavailable")
		return
	}
	p := currentExecution(r)
	if r.PathValue("id") != p.Claims.ConversationID || r.PathValue("message_id") != p.Claims.MessageID || r.PathValue("agent_id") != p.Claims.AgentID {
		writeErr(w, 403, "input is outside credential scope")
		return
	}
	result, err := s.service.ConversationService.CompleteMessages(r.Context(), p.DeviceID, p.Claims.ConversationID, p.Claims.MessageID, p.Claims.AgentID, r.URL.Query().Get("session_id"))
	if err != nil {
		s.serviceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
