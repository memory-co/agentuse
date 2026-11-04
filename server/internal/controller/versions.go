package controller

import (
	"net/http"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func (s *Server) handlePublishAgentVersion(w http.ResponseWriter, r *http.Request) {
	var body agentuse.PublishAgentVersionRequest
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	v, err := s.service.PublishAgentVersion(r.Context(), currentUser(r), r.PathValue("id"), body.BaseVersionID, body.AgentConfig)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	snapshot, err := s.service.GetAgentVersion(r.Context(), currentUser(r), v.ID, v.CurrentVersionID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	status := http.StatusCreated
	if v.CurrentVersionID == body.BaseVersionID {
		status = http.StatusOK
	}
	writeJSON(w, status, snapshot)
}
func (s *Server) handleAgentVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.service.AgentVersions(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, rows)
}
func (s *Server) handleAgentVersion(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.GetAgentVersion(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("version_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
