// Package server 是 agentuse 服务：网页、HTTP API、setup.sh 和 daemon 的 WebSocket。
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/store"
)

//go:embed web
var webFS embed.FS

type Config struct {
	// PublicURL 是用户电脑访问服务的地址，写进安装命令和 setup.sh。空 = 按请求的 Host 推断。
	PublicURL string
	// DistDir 放各平台的 agentuse 二进制(agentuse-<os>-<arch>),setup.sh 从这里下载。
	DistDir string
	Logger  *log.Logger
}

type Server struct {
	cfg   Config
	store *store.Store
	hub   *hub
	log   *log.Logger
	mux   *http.ServeMux
}

func New(st *store.Store, cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	s := &Server{cfg: cfg, store: st, log: cfg.Logger, mux: http.NewServeMux()}
	s.hub = newHub(s)
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Run 跑后台任务(收回过期租约),直到 ctx 结束。
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			users, err := s.store.RequeueExpired(ctx)
			if err != nil {
				s.log.Printf("requeue expired: %v", err)
				continue
			}
			for _, u := range users {
				s.hub.kickUser(u)
			}
		}
	}
}

func (s *Server) routes() {
	static, _ := fs.Sub(webFS, "web")
	s.mux.Handle("GET /", http.FileServerFS(static))
	s.mux.HandleFunc("GET /login", s.handleLoginLink)
	s.mux.HandleFunc("GET /setup.sh", s.handleSetupScript)
	s.mux.HandleFunc("GET /download/{file}", s.handleDownload)

	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/me", s.auth(s.handleMe))
	s.mux.HandleFunc("GET /api/users", s.admin(s.handleListUsers))
	s.mux.HandleFunc("POST /api/users", s.admin(s.handleCreateUser))
	s.mux.HandleFunc("POST /api/users/{id}/token", s.admin(s.handleResetUserToken))
	s.mux.HandleFunc("GET /api/agents", s.auth(s.handleListAgents))
	s.mux.HandleFunc("POST /api/agents", s.auth(s.handleCreateAgent))
	s.mux.HandleFunc("DELETE /api/agents/{name}", s.auth(s.handleDeleteAgent))
	s.mux.HandleFunc("GET /api/daemons", s.auth(s.handleListDaemons))
	s.mux.HandleFunc("POST /api/daemons", s.auth(s.handleCreateDaemon))
	s.mux.HandleFunc("DELETE /api/daemons/{id}", s.auth(s.handleDeleteDaemon))
	s.mux.HandleFunc("POST /api/daemons/{id}/reset", s.auth(s.handleResetDaemon))
	s.mux.HandleFunc("PUT /api/daemons/{id}/agents/{agent}", s.auth(s.handleToggleAgent))
	s.mux.HandleFunc("GET /api/sessions", s.auth(s.handleListSessions))
	s.mux.HandleFunc("POST /api/sessions", s.auth(s.handleCreateSession))
	s.mux.HandleFunc("GET /api/sessions/{id}", s.auth(s.handleGetSession))
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.auth(s.handleDeleteSession))
	s.mux.HandleFunc("POST /api/sessions/{id}/messages", s.auth(s.handleAddMessage))

	s.mux.HandleFunc("POST "+protocol.RegisterPath, s.handleRegister)
	s.mux.HandleFunc("GET "+protocol.WSPath, s.hub.handleWS)
	s.mux.HandleFunc("GET "+protocol.MePath, s.handleDaemonMe)
}

// baseURL 是安装命令里的服务地址。
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	return scheme + "://" + host
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) storeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "不存在")
		return
	}
	if errors.Is(err, store.ErrSessionBusy) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeErr(w, http.StatusBadRequest, err.Error())
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("请求体不是合法 JSON")
	}
	return nil
}
