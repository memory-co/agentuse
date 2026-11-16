package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/memory-co/agentuse/server/internal/database"
	upgradeservice "github.com/memory-co/agentuse/server/internal/service/upgrade"
)

func (s *Server) upgradeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, upgradeservice.ErrUnavailable) {
		status = http.StatusServiceUnavailable
	}
	if errors.Is(err, database.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeErr(w, status, err.Error())
}
func (s *Server) handleUpgradeState(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeErr(w, 400, "limit 必须介于 1 和 100")
			return
		}
	}
	out, err := s.service.UpgradeService.State(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.upgradeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleLatestUpgrade(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.UpgradeService.Latest(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleStartUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		writeErr(w, 400, "升级请求无需请求体")
		return
	}
	out, err := s.service.UpgradeService.Start(r.Context(), currentUser(r).ID)
	if err != nil {
		s.upgradeErr(w, err)
		return
	}
	w.Header().Set("Location", "/api/upgrades/"+out.ID)
	writeJSON(w, http.StatusAccepted, out)
}
func (s *Server) handleGetUpgrade(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.UpgradeService.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.upgradeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}
