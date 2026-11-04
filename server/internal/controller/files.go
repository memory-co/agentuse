package controller

import (
	"io"
	"net/http"
	"strconv"
)

func (s *Server) handleConversationFile(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	if _, err := s.service.GetSession(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		s.storeErr(w, err)
		return
	}
	if s.service.Files == nil {
		writeErr(w, 503, "file storage unavailable")
		return
	}
	key := r.PathValue("id") + "/" + r.PathValue("path")
	switch r.Method {
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.AttachmentMaxBytes)
		if err := s.service.Files.Put(r.Context(), "conversations", key, r.Body, false); err != nil {
			s.storeErr(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		stream, size, err := s.service.Files.Get(r.Context(), "conversations", key)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		defer stream.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		_, _ = io.Copy(w, stream)
	}
}
