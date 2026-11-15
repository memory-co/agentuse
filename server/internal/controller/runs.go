package controller

import (
	"net/http"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !s.conversationInScope(w, r, r.PathValue("id"), false) {
		return
	}
	snapshot, err := s.service.ConversationService.ReadConversation(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.serviceErr(w, err)
		return
	}
	for _, run := range snapshot.Runs {
		if run.ID != r.PathValue("run_id") {
			continue
		}
		events := []agentuse.ConversationEvent{}
		inputs := []agentuse.RunInput{}
		for _, event := range snapshot.Events {
			if event.RunID == run.ID {
				events = append(events, event)
			}
		}
		for _, input := range snapshot.RunInputs {
			if input.RunID == run.ID {
				inputs = append(inputs, input)
			}
		}
		writeJSON(w, 200, agentuse.RunDetail{Run: run, Events: events, Inputs: inputs})
		return
	}
	s.serviceErr(w, database.ErrNotFound)
}
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.conversationInScope(w, r, r.PathValue("id"), false) {
		return
	}
	run, err := s.service.RunService.CancelRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		s.serviceErr(w, err)
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
		s.serviceErr(w, err)
		return
	}
	writeJSON(w, 202, answerRunResponse{State: "pending"})
}
