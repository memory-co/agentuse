// Package server 是 agentuse 服务：网页、HTTP API、setup.sh 和 daemon 的 WebSocket。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/store"
)

type Config struct {
	AttachmentsDir            string
	AttachmentMaxBytes        int64
	AttachmentSessionMaxBytes int64
	// PublicURL 是用户电脑访问服务的地址，写进安装命令和 setup.sh。空 = 按请求的 Host 推断。
	PublicURL string
	// DistDir 放各平台的 agentuse 二进制(agentuse-<os>-<arch>),setup.sh 从这里下载。
	DistDir string
	Logger  *log.Logger
}

type Server struct {
	attachmentMu sync.RWMutex
	cfg          Config
	store        *store.Store
	hub          *hub
	log          *log.Logger
	mux          *http.ServeMux
}

func New(st *store.Store, cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.AttachmentsDir == "" {
		home, _ := os.UserHomeDir()
		cfg.AttachmentsDir = filepath.Join(home, ".agentuse", "attachments")
	}
	if cfg.AttachmentMaxBytes <= 0 {
		cfg.AttachmentMaxBytes = 100 << 20
	}
	if cfg.AttachmentSessionMaxBytes <= 0 {
		cfg.AttachmentSessionMaxBytes = 1 << 30
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
	if err := s.CleanupAttachments(ctx); err != nil {
		s.log.Printf("attachment cleanup: %v", err)
	}
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			if err := s.CleanupAttachments(ctx); err != nil {
				s.log.Printf("attachment cleanup: %v", err)
			}
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
	s.mux.HandleFunc("GET /", s.handleWeb)
	s.mux.HandleFunc("GET /login", s.handleLoginLink)
	s.mux.HandleFunc("GET /setup.sh", s.handleSetupScript)
	s.mux.HandleFunc("GET /download/{file}", s.handleDownload)

	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/me", s.auth(s.handleMe))
	s.mux.HandleFunc("GET /api/users", s.admin(s.handleListUsers))
	s.mux.HandleFunc("POST /api/users", s.admin(s.handleCreateUser))
	s.mux.HandleFunc("POST /api/users/{id}/token", s.admin(s.handleResetUserToken))
	s.mux.HandleFunc("GET /api/harnesses", s.auth(s.handleListHarnesses))
	s.mux.HandleFunc("POST /api/harnesses", s.auth(s.handleCreateHarness))
	s.mux.HandleFunc("DELETE /api/harnesses/{name}", s.auth(s.handleDeleteHarness))
	s.mux.HandleFunc("GET /api/agents", s.workAuth(s.handleListAgents))
	s.mux.HandleFunc("POST /api/agents", s.auth(s.handleCreateAgent))
	s.mux.HandleFunc("GET /api/agents/{id}", s.auth(s.handleGetAgent))
	s.mux.HandleFunc("PATCH /api/agents/{id}", s.auth(s.handleUpdateAgent))
	s.mux.HandleFunc("DELETE /api/agents/{id}", s.auth(s.handleDeleteAgent))
	s.mux.HandleFunc("GET /api/daemons", s.auth(s.handleListDaemons))
	s.mux.HandleFunc("POST /api/daemons", s.auth(s.handleCreateDaemon))
	s.mux.HandleFunc("DELETE /api/daemons/{id}", s.auth(s.handleDeleteDaemon))
	s.mux.HandleFunc("POST /api/daemons/{id}/reset", s.auth(s.handleResetDaemon))
	s.mux.HandleFunc("PUT /api/daemons/{id}/harnesses/{harness}", s.auth(s.handleToggleHarness))
	s.mux.HandleFunc("GET /api/works", s.auth(s.handleListWorks))
	s.mux.HandleFunc("POST /api/works", s.auth(s.handleCreateWork))
	s.mux.HandleFunc("GET /api/works/{id}", s.auth(s.handleGetWork))
	s.mux.HandleFunc("PATCH /api/works/{id}", s.auth(s.handleUpdateWork))
	s.mux.HandleFunc("DELETE /api/works/{id}", s.auth(s.handleDeleteWork))
	// 带 workAuth 的几个，协调者也能用工作令牌调(docs/v2/apis/works.md)
	s.mux.HandleFunc("GET /api/sessions", s.workAuth(s.handleListSessions))
	s.mux.HandleFunc("POST /api/sessions", s.workAuth(s.handleCreateSession))
	s.mux.HandleFunc("GET /api/sessions/{id}", s.workAuth(s.handleGetSession))
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.auth(s.handleDeleteSession))
	s.mux.HandleFunc("POST /api/sessions/{id}/messages", s.workAuth(s.handleAddMessage))
	s.mux.HandleFunc("POST /api/sessions/{id}/resolve", s.workAuth(s.handleResolveSession))
	s.mux.HandleFunc("POST /api/sessions/{id}/reopen", s.auth(s.handleReopenSession))
	s.mux.HandleFunc("POST /api/sessions/{id}/questions/{qid}/answer", s.auth(s.handleAnswerQuestion))

	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"POST /api/sessions/{id}/attachments", s.handleUploadAttachment},
		{"GET /api/sessions/{id}/attachments", s.handleListAttachments},
		{"GET /api/sessions/{id}/attachments/{attachment_id}", s.handleGetAttachment},
		{"GET /api/sessions/{id}/attachments/{attachment_id}/content", s.handleAttachmentContent},
		{"DELETE /api/sessions/{id}/attachments/{attachment_id}", s.handleDeleteAttachment},
	} {
		s.mux.HandleFunc(route.pattern, s.attachmentAuth(route.handler))
	}
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
	if errors.Is(err, store.ErrSessionBusy) || errors.Is(err, store.ErrQuestionClosed) || errors.Is(err, store.ErrSessionState) {
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
