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

// HeaderDevice 是 daemon 报自己设备 id 的头,配合 Authorization: Bearer <设备密钥>。
const HeaderDevice = "X-AgentUse-Device"

const maxBody = 1 << 20

// ServerConfig 是 HTTP 接口的认证配置。
type ServerConfig struct {
	AdminKey string // 管理接口的 Bearer 密钥;空 = 关闭管理接口
}

// Server 是 hub 的 HTTP 接口,端点见 docs/apis/。
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
	// 平台 → hub(平台密钥签名)
	mux.HandleFunc("POST /v1/platforms/{platform}/notify", s.notify)
	mux.HandleFunc("POST /v1/platforms/{platform}/agents", s.platform(s.createAgent))
	mux.HandleFunc("GET /v1/platforms/{platform}/agents", s.platform(s.listAgents))
	mux.HandleFunc("GET /v1/platforms/{platform}/agents/{agent}", s.platform(s.getAgent))
	mux.HandleFunc("PATCH /v1/platforms/{platform}/agents/{agent}", s.platform(s.patchAgent))
	mux.HandleFunc("DELETE /v1/platforms/{platform}/agents/{agent}", s.platform(s.deleteAgent))
	mux.HandleFunc("POST /v1/platforms/{platform}/agents/{agent}/connect-code", s.platform(s.connectCode))
	mux.HandleFunc("DELETE /v1/platforms/{platform}/devices/{device}", s.platform(s.revokeDevice))
	// 用户电脑 → hub:兑换接入码(接入码即凭据)
	mux.HandleFunc("POST /v1/connect", s.connectDevice)
	// daemon → hub(设备密钥)
	mux.HandleFunc("GET /v1/device", s.device(s.deviceInfo))
	mux.HandleFunc("POST /v1/device/disconnect", s.device(s.disconnect))
	mux.HandleFunc("GET /v1/daemon/connect", s.device(s.connect))
	mux.HandleFunc("POST /v1/daemon/claim", s.device(s.claim))
	mux.HandleFunc("POST /v1/runs/{run}/heartbeat", s.device(s.heartbeat))
	mux.HandleFunc("POST /v1/runs/{run}/complete", s.device(s.complete))
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

// platformHandler 是验过平台签名的请求:平台 id 和请求体。
type platformHandler func(w http.ResponseWriter, r *http.Request, platform string, body []byte)

// platform 验平台管理接口的签名:签的是 signature.Request(方法, 路径含查询串, 请求体)。
func (s *Server) platform(fn platformHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		if err := signature.Verify(r.Header, p.Secret, signature.Request(r.Method, r.URL.RequestURI(), body), time.Now()); err != nil {
			writeError(w, http.StatusUnauthorized, "bad_signature")
			return
		}
		fn(w, r, p.ID, body)
	}
}

// publicURL 是写进接入命令的 hub 地址:配置了就用配置的,否则按这次请求推断。
func (s *Server) publicURL(r *http.Request) string {
	if s.hub.opts.PublicURL != "" {
		return s.hub.opts.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) withCommand(r *http.Request, info ConnectInfo) ConnectInfo {
	info.Command = ConnectCommand(s.publicURL(r), info.Code)
	return info
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request, platform string, body []byte) {
	var req NewAgent
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	a, info, err := s.hub.CreateAgent(r.Context(), platform, req)
	if err != nil {
		writeHubError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": a, "connect": s.withCommand(r, info)})
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request, platform string, _ []byte) {
	list, err := s.hub.Agents(r.Context(), platform, r.URL.Query().Get("owner"))
	if err != nil {
		writeHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": list})
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request, platform string, _ []byte) {
	a, err := s.hub.Agent(r.Context(), platform, r.PathValue("agent"))
	if err != nil {
		writeHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) patchAgent(w http.ResponseWriter, r *http.Request, platform string, body []byte) {
	var patch AgentPatch
	if err := json.Unmarshal(body, &patch); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	a, err := s.hub.UpdateAgent(r.Context(), platform, r.PathValue("agent"), patch)
	if err != nil {
		writeHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request, platform string, _ []byte) {
	if err := s.hub.DeleteAgent(r.Context(), platform, r.PathValue("agent")); err != nil {
		writeHubError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) connectCode(w http.ResponseWriter, r *http.Request, platform string, _ []byte) {
	info, err := s.hub.NewConnectCode(r.Context(), platform, r.PathValue("agent"))
	if err != nil {
		writeHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withCommand(r, info))
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request, platform string, _ []byte) {
	if err := s.hub.RevokeDevice(r.Context(), platform, r.PathValue("device")); err != nil {
		writeHubError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) connectDevice(w http.ResponseWriter, r *http.Request) {
	var req ConnectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	res, err := s.hub.Connect(r.Context(), req)
	if err != nil {
		writeHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeHubError 把 hub 的错误映射成 HTTP 状态和错误码。
func writeHubError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnknownPlatform):
		writeError(w, http.StatusNotFound, "unknown_platform")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid", "message": strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")})
	case errors.Is(err, ErrHandleTaken):
		writeError(w, http.StatusConflict, "handle_taken")
	case errors.Is(err, ErrBadCode):
		writeError(w, http.StatusUnauthorized, "bad_code")
	case errors.Is(err, ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	default:
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

// deviceHandler 是认过设备密钥的请求。
type deviceHandler func(w http.ResponseWriter, r *http.Request, d models.Device)

// device 认设备:X-AgentUse-Device: <设备 id> + Authorization: Bearer <设备密钥>。
func (s *Server) device(fn deviceHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := s.hub.AuthDevice(r.Context(), r.Header.Get(HeaderDevice), bearer(r))
		if err != nil {
			writeHubError(w, err)
			return
		}
		fn(w, r, d)
	}
}

func (s *Server) deviceInfo(w http.ResponseWriter, r *http.Request, d models.Device) {
	view, agents, err := s.hub.DeviceInfo(r.Context(), d.ID)
	if err != nil {
		writeHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"platform": d.Platform, "device": view, "agents": agents})
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request, d models.Device) {
	var body struct {
		Agent string `json:"agent"` // handle
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil || body.Agent == "" {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	if err := s.hub.Disconnect(r.Context(), d, body.Agent); err != nil {
		writeHubError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request, d models.Device) {
	c, err := s.hub.Claim(r.Context(), d.ID)
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

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request, d models.Device) {
	var body struct {
		Attempt int `json:"attempt"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	run, err := s.hub.Heartbeat(r.Context(), r.PathValue("run"), d.ID, body.Attempt)
	writeRunResult(w, run, err)
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request, d models.Device) {
	var c Completion
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	c.DeviceID = d.ID
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
		AgentID:   q.Get("agent"),
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
