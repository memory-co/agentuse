package server

import (
	"net/http"

	"github.com/memory-co/agentuse/server/frontend"
)

var static = http.FileServerFS(frontend.FS())

// handleWeb 托管 frontend/dist 里的网页。没构建前端时给一句提示,API 不受影响。
func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if !frontend.Built() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("网页还没构建:cd frontend && npm ci && npm run build,然后重新编译 agentuse-server(或直接 make build)。\n"))
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	static.ServeHTTP(w, r)
}
