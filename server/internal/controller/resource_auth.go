package controller

import (
	"net/http"
	"strings"
)

func resourceCredential(r *http.Request) bool {
	token := requestToken(r)
	return strings.HasPrefix(token, "dk_")
}
func (s *Server) resourceAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if resourceCredential(r) {
			if r.Header.Get(actAsHeader) != "" {
				writeErr(w, 403, "device credentials cannot impersonate users")
				return
			}
			next(w, r)
			return
		}
		s.auth(next)(w, r)
	}
}
