package controller

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	runservice "github.com/memory-co/agentuse/server/internal/service/run"
)

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !s.conversationInScope(w, r, r.PathValue("id"), false) {
		return
	}
	metadata := false
	if view, ok := r.URL.Query()["view"]; ok {
		if len(view) != 1 || view[0] != "metadata" {
			writeErr(w, http.StatusBadRequest, "view must be metadata")
			return
		}
		metadata = true
	}
	out, err := s.service.ConversationService.ReadRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"), metadata)
	if err != nil {
		s.serviceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	conversation := r.PathValue("id")
	if !s.conversationInScope(w, r, conversation, false) {
		return
	}
	limit := runservice.DefaultEventPageSize
	var after int64
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			writeErr(w, http.StatusBadRequest, "invalid "+key)
			return
		}
		switch key {
		case "limit":
			value, err := strconv.Atoi(values[0])
			if err != nil || value < 1 || value > runservice.MaxEventPageSize {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", runservice.MaxEventPageSize))
				return
			}
			limit = value
		case "cursor":
			value, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || value <= 0 {
				writeErr(w, http.StatusBadRequest, "invalid cursor")
				return
			}
			after = value
		default:
			writeErr(w, http.StatusBadRequest, "unknown query parameter: "+key)
			return
		}
	}
	page, err := s.service.RunService.PageEvents(r.Context(), currentUser(r), conversation, r.PathValue("run_id"), after, limit)
	if err != nil {
		s.serviceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleRunEventPayload(w http.ResponseWriter, r *http.Request) {
	conversation := r.PathValue("id")
	if !s.conversationInScope(w, r, conversation, false) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("event_id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid event id")
		return
	}
	payload, err := s.service.RunService.EventPayload(r.Context(), currentUser(r), conversation, r.PathValue("run_id"), id)
	if err != nil {
		s.serviceErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="run-event-%d.json"`, id))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, payload)
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
