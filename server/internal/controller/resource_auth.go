package controller

import (
	"github.com/memory-co/agentuse/internal/access"
	"net/http"
	"strings"
)

func resourceCredential(r *http.Request) bool {
	return strings.HasPrefix(requestToken(r), access.Prefix)
}
func (s *Server) resourceAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if resourceCredential(r) {
			if r.Header.Get(actAsHeader) != "" {
				writeErr(w, 403, "execution credentials cannot impersonate users")
				return
			}
			if _, err := s.service.RunService.AccessForRequest(r.Context(), requestToken(r)); err != nil {
				writeErr(w, 403, "execution access unavailable")
				return
			}
			next(w, r)
			return
		}
		s.auth(next)(w, r)
	}
}
