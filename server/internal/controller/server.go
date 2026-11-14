// Package controller serves the HTTP API, management UI and installation endpoints.
package controller

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/server/internal/service"
	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

type Config struct {
	FileStore                      *fileservice.Service
	FilesDir                       string
	ClientHandler                  http.Handler
	AttachmentMaxBytes             int64
	AttachmentConversationMaxBytes int64
	// PublicURL 是用户电脑访问服务的地址，写进安装命令和 setup.sh。空 = 按请求的 Host 推断。
	PublicURL string
	// DistDir 放各平台的 agentuse 二进制(agentuse-<os>-<arch>),setup.sh 从这里下载。
	DistDir string
	Logger  *log.Logger
}

type Server struct {
	cfg     Config
	service *service.Service
	log     *log.Logger
	mux     *http.ServeMux
}

func New(st *service.Service, cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	if cfg.AttachmentMaxBytes <= 0 {
		cfg.AttachmentMaxBytes = 100 << 20
	}
	if cfg.AttachmentConversationMaxBytes <= 0 {
		cfg.AttachmentConversationMaxBytes = 1 << 30
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	s := &Server{cfg: cfg, log: cfg.Logger, mux: http.NewServeMux()}
	s.service = st
	if st != nil {
		s.service.ConversationService.ConfigureConversationAttachments(cfg.AttachmentMaxBytes, cfg.AttachmentConversationMaxBytes, cfg.Logger)
	}
	if s.cfg.ClientHandler == nil {
		s.cfg.ClientHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeErr(w, 503, "device transport not configured") })
	}
	if st != nil {
		files := cfg.FileStore
		if files == nil {
			dir := cfg.FilesDir
			if dir == "" {
				home, _ := os.UserHomeDir()
				dir = filepath.Join(home, ".agentuse", "files")
			}
			var err error
			files, err = fileservice.NewLocal(dir)
			if err != nil {
				cfg.Logger.Printf("file storage: %v", err)
			}
		}
		st.ConfigureFiles(files)
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleWeb)
	s.mux.HandleFunc("/api/", s.handleUnknownAPI)
	s.mux.HandleFunc("GET /login", s.handleLoginLink)
	s.mux.HandleFunc("GET /setup.sh", s.handleSetupScript)
	s.mux.HandleFunc("GET /download/{file}", s.handleDownload)

	s.registerAPI()
}

// Keep missing resources at 404, including removed namespaces, while retaining
// 405 and Allow for a known resource requested with an unsupported method.
func (s *Server) handleUnknownAPI(w http.ResponseWriter, r *http.Request) {
	var allowed []string
	for _, method := range []string{"DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"} {
		probe := r.Clone(r.Context())
		probe.Method = method
		_, pattern := s.mux.Handler(probe)
		if strings.HasPrefix(pattern, method+" ") || (method == "HEAD" && strings.HasPrefix(pattern, "GET ")) {
			allowed = append(allowed, method)
		}
	}
	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeErr(w, http.StatusMethodNotAllowed, "HTTP method not allowed")
		return
	}
	writeErr(w, http.StatusNotFound, "API endpoint not found")
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
	if errors.Is(err, service.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "不存在")
		return
	}
	if errors.Is(err, skillservice.ErrConflict) || errors.Is(err, service.ErrConversationBusy) || errors.Is(err, service.ErrQuestionClosed) || errors.Is(err, service.ErrConversationState) || errors.Is(err, service.ErrAttachmentReferenced) || errors.Is(err, service.ErrAttachmentPathConflict) || errors.Is(err, fileservice.ErrExists) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeErr(w, http.StatusBadRequest, err.Error())
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("请求体不是合法 JSON")
	}
	return nil
}

func readMessageJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("请求体必须是单个 JSON 对象")
	}
	return nil
}
