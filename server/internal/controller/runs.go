package controller

import (
	"net/http"

	agentuse "github.com/memory-co/agentuse/sdk/golang"

	"github.com/memory-co/agentuse/server/internal/service"
)

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !s.conversationInScope(w, r, r.PathValue("id"), false) {
		return
	}
	run, err := s.service.RunService.GetRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	events, err := s.service.RunService.RunEvents(r.Context(), run.ConversationID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	filtered := []service.ConversationEvent{}
	for _, e := range events {
		if e.RunID == run.ID {
			filtered = append(filtered, e)
		}
	}
	writeJSON(w, 200, agentuse.RunDetail{Run: run, Events: filtered})
}
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.conversationInScope(w, r, r.PathValue("id"), false) {
		return
	}
	run, err := s.service.RunService.CancelRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, run)
}
func (s *Server) handleAnswerRun(w http.ResponseWriter, r *http.Request) {
	if !s.conversationInScope(w, r, r.PathValue("id"), false) {
		return
	}
	var body answerRunBody
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.service.RunService.ReplyRequest(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"), r.PathValue("request_id"), body.Answer); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 202, answerRunResponse{State: "pending"})
}
