package controller

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/memory-co/agentuse/internal/skillbundle"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	rows, err := s.service.Skills.List(r.Context(), currentUser(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, rows)
}
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.Skills.Get(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handlePublishSkill(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, skillbundle.MaxArchive+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, 400, "invalid skill ZIP upload: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "multipart file is required")
		return
	}
	defer file.Close()
	v, err := s.service.Skills.Publish(r.Context(), currentUser(r), r.PathValue("id"), r.FormValue("base_version_id"), file)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) handleSkillVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.service.Skills.Versions(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, rows)
}
func (s *Server) handleSkillVersion(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.GetSkillVersion(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("version_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if err := s.service.Skills.Delete(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) handleSkillPackage(w http.ResponseWriter, r *http.Request) {
	serve := func(v agentuse.SkillVersion) {
		if s.service.Files == nil {
			writeErr(w, 503, "file storage unavailable")
			return
		}
		stream, size, err := s.service.Files.Get(r.Context(), "skills", v.PackagePath)
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, 404, "skill package missing")
			return
		}
		if err != nil {
			s.storeErr(w, err)
			return
		}
		defer stream.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("ETag", `"`+v.SHA256+`"`)
		w.Header().Set("Content-Disposition", `attachment; filename="skill.zip"`)
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		_, _ = io.Copy(w, stream)
	}
	token := requestToken(r)
	if strings.HasPrefix(token, "dk_") || strings.HasPrefix(token, "wt_") {
		if r.Header.Get(actAsHeader) != "" {
			writeErr(w, 403, "device/work tokens cannot impersonate users")
			return
		}
		v, err := s.service.SkillForCredential(r.Context(), token, r.PathValue("id"), r.PathValue("version_id"))
		if err != nil {
			s.storeErr(w, err)
			return
		}
		serve(v)
		return
	}
	s.auth(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.service.GetSkillVersion(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("version_id"))
		if err != nil {
			s.storeErr(w, err)
			return
		}
		serve(v)
	})(w, r)
}
