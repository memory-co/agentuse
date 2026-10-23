package hub

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/internal/services/token"
	"github.com/memory-co/agentuse/internal/store"
	"github.com/memory-co/agentuse/pkg/signature"
)

// HeaderDaemon 是 daemon 报自己 id 的头,配合 Authorization: Bearer <daemon key>。
const HeaderDaemon = "X-AgentUse-Daemon"

const maxBody = 1 << 20

// ServerConfig 是 HTTP 接口的认证配置。
type ServerConfig struct {
	DaemonKeys map[string]string // daemon id → 密钥
	AdminKey   string            // 管理接口的 Bearer 密钥;空 = 关闭管理接口
}

// Server 是 hub 的 HTTP 接口,端点见 docs/v1/apis/。
type Server struct {
	hub *Hub
	cfg ServerConfig
}

func NewServer(h *Hub, cfg ServerConfig) *Server {
	return &Server{hub: h, cfg: cfg}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// 平台 → hub
	mux.HandleFunc("POST /v1/platforms/{platform}/notify", s.notify)
	// daemon → hub
	mux.HandleFunc("GET /v1/daemon/connect", s.connect)
	mux.HandleFunc("POST /v1/daemon/claim", s.claim)
	mux.HandleFunc("POST /v1/runs/{run}/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /v1/runs/{run}/complete", s.complete)
	// agent → hub(运行令牌)
	mux.HandleFunc("GET /v1/run", s.runInfo)
	mux.HandleFunc("POST /v1/run/reply", s.write(models.EventReply))
	mux.HandleFunc("POST /v1/run/status", s.write(models.EventStatus))
	mux.HandleFunc("POST /v1/run/activity", s.write(models.EventActivity))
	// 实例主人(管理密钥)
	mux.HandleFunc("GET /v1/admin/sessions", s.admin(s.listSessions))
	mux.HandleFunc("GET /v1/admin/runs", s.admin(s.listRuns))
	mux.HandleFunc("GET /v1/admin/runs/{run}", s.admin(s.getRun))
	return mux
}

func (s *Server) notify(w http.ResponseWriter, r *http.Request) {
	p, err := s.hub.Platform(r.PathValue("platform"))
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown_platform")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	if err := signature.Verify(r.Header, p.Secret, body, time.Now()); err != nil {
		writeError(w, http.StatusUnauthorized, "bad_signature")
		return
	}
	var n Notification
	if err := json.Unmarshal(body, &n); err != nil || n.Thread == "" || n.ID == "" {
		writeError(w, http.StatusBadRequest, "bad_notification")
		return
	}
	res, err := s.hub.Notify(r.Context(), p.ID, n)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	status := http.StatusOK
	if res.Woken {
		status = http.StatusAccepted
	}
	writeJSON(w, status, res)
}

func (s *Server) daemonID(r *http.Request) (string, bool) {
	id := r.Header.Get(HeaderDaemon)
	key, ok := s.cfg.DaemonKeys[id]
	if id == "" || !ok {
		return "", false
	}
	got := bearer(r)
	return id, got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	id, ok := s.daemonID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	c, err := s.hub.Claim(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := s.daemonID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Attempt int `json:"attempt"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	run, err := s.hub.Heartbeat(r.Context(), r.PathValue("run"), id, body.Attempt)
	writeRunResult(w, run, err)
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.daemonID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var c Completion
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	c.DaemonID = id
	run, err := s.hub.Complete(r.Context(), r.PathValue("run"), c)
	writeRunResult(w, run, err)
}

func writeRunResult(w http.ResponseWriter, run models.Run, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrRunState):
		writeError(w, http.StatusConflict, "run_state")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal")
	default:
		writeJSON(w, http.StatusOK, run)
	}
}

func (s *Server) admin(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := bearer(r)
		if s.cfg.AdminKey == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		fn(w, r)
	}
}

func queryLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	out, err := s.hub.Sessions(r.Context(), r.URL.Query().Get("platform"), queryLimit(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := s.hub.Runs(r.Context(), store.RunFilter{
		Platform:  q.Get("platform"),
		SessionID: q.Get("session"),
		Status:    models.RunStatus(q.Get("status")),
		Limit:     queryLimit(r),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.hub.Run(r.Context(), r.PathValue("run"))
	writeRunResult(w, run, err)
}

func (s *Server) claims(w http.ResponseWriter, r *http.Request) (token.Claims, bool) {
	c, err := s.hub.Verify(bearer(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "bad_token")
		return c, false
	}
	return c, true
}

func (s *Server) runInfo(w http.ResponseWriter, r *http.Request) {
	c, ok := s.claims(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) write(t models.EventType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.claims(w, r)
		if !ok {
			return
		}
		var ev models.Event
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&ev); err != nil {
			writeError(w, http.StatusBadRequest, "bad_body")
			return
		}
		ev.Type = t
		err := s.hub.Write(r.Context(), c, ev)
		switch {
		case errors.Is(err, ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, ErrRunState):
			writeError(w, http.StatusConflict, "run_state")
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found")
		case err != nil:
			writeError(w, http.StatusBadGateway, "callback_failed")
		default:
			writeJSON(w, http.StatusOK, map[string]any{})
		}
	}
}

func bearer(r *http.Request) string {
	v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
