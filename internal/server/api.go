package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/store"
)

const cookieName = "agentuse_token"

type userKey struct{}

func currentUser(r *http.Request) store.User { return r.Context().Value(userKey{}).(store.User) }

func requestToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}

// auth 要求登录：网页用 cookie,脚本用 Authorization: Bearer <用户令牌>。
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := requestToken(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "未登录")
			return
		}
		u, err := s.store.UserByToken(r.Context(), tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "令牌无效")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).IsAdmin() {
			writeErr(w, http.StatusForbidden, "需要管理员")
			return
		}
		h(w, r)
	})
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.baseURL(r), "https://"),
	})
}

// handleLoginLink:GET /login?token=… 是服务启动时打印的登录链接。
func (s *Server) handleLoginLink(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if _, err := s.store.UserByToken(r.Context(), tok); err != nil {
		http.Redirect(w, r, "/?login=invalid", http.StatusFound)
		return
	}
	s.setCookie(w, r, tok, 365*24*3600)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.store.UserByToken(r.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "令牌无效")
		return
	}
	s.setCookie(w, r, strings.TrimSpace(req.Token), 365*24*3600)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.setCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Role == "" {
		req.Role = store.RoleUser
	}
	u, token, err := s.store.CreateUser(r.Context(), req.ID, req.Name, req.Role)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user": u, "token": token, "login_url": s.baseURL(r) + "/login?token=" + token,
	})
}

func (s *Server) handleResetUserToken(w http.ResponseWriter, r *http.Request) {
	u, token, err := s.store.ResetUserToken(r.Context(), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": u, "token": token, "login_url": s.baseURL(r) + "/login?token=" + token,
	})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.store.CreateCustomAgent(r.Context(), currentUser(r).ID, req.Name, req.DisplayName, req.Description)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteCustomAgent(r.Context(), currentUser(r), r.PathValue("name")); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// daemonView 是页面上一台 daemon:待注册的附带安装命令。
type daemonView struct {
	store.Daemon
	SetupCommand string `json:"setup_command,omitempty"`
}

func (s *Server) view(r *http.Request, d store.Daemon) daemonView {
	v := daemonView{Daemon: d}
	if d.SetupToken != "" {
		v.SetupCommand = setupCommand(s.baseURL(r), d.SetupToken)
	}
	return v
}

func setupCommand(base, token string) string {
	return `curl -fsSL "` + base + `/setup.sh" -o setup.sh && TOKEN="` + token + `" bash setup.sh`
}

func (s *Server) handleListDaemons(w http.ResponseWriter, r *http.Request) {
	ds, err := s.store.ListDaemons(r.Context(), currentUser(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := make([]daemonView, 0, len(ds))
	for _, d := range ds {
		out = append(out, s.view(r, d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateDaemon(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	d, err := s.store.CreateDaemon(r.Context(), currentUser(r).ID, req.Name)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.view(r, d))
}

func (s *Server) handleDeleteDaemon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.store.GetDaemon(r.Context(), currentUser(r), id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if err := s.store.DeleteDaemon(r.Context(), currentUser(r), id); err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.disconnect(id)
	s.hub.kickUser(d.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetDaemon(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.ResetDaemonSetup(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.disconnect(d.ID)
	writeJSON(w, http.StatusOK, s.view(r, d))
}

func (s *Server) handleToggleAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(r)
	id := r.PathValue("id")
	if err := s.store.SetDaemonAgentEnabled(r.Context(), u, id, r.PathValue("agent"), req.Enabled); err != nil {
		s.storeErr(w, err)
		return
	}
	d, err := s.store.GetDaemon(r.Context(), u, id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if req.Enabled {
		s.hub.kickUser(d.UserID)
	}
	writeJSON(w, http.StatusOK, s.view(r, d))
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks(r.Context(), currentUser(r), 100)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// handleCreateTask 建任务。input 可以直接给 JSON,也可以只给 prompt(和可选的 cwd)。
func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent  string          `json:"agent"`
		Input  json.RawMessage `json:"input"`
		Prompt string          `json:"prompt"`
		Cwd    string          `json:"cwd"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Agent == "" {
		writeErr(w, http.StatusBadRequest, "agent 必填")
		return
	}
	input := req.Input
	if len(input) == 0 {
		if strings.TrimSpace(req.Prompt) == "" {
			writeErr(w, http.StatusBadRequest, "prompt 或 input 必填")
			return
		}
		input, _ = json.Marshal(protocol.TaskInput{Prompt: req.Prompt, Cwd: req.Cwd})
	}
	u := currentUser(r)
	t, err := s.store.CreateTask(r.Context(), u.ID, req.Agent, input)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.kickUser(u.ID)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	events, err := s.store.TaskEvents(r.Context(), t.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": t, "events": events})
}

// handleDaemonMe:daemon 用密钥查自己，验证密钥是否还有效。
func (s *Server) handleDaemonMe(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := s.store.DaemonByKey(r.Context(), key)
	if key == "" || err != nil {
		writeErr(w, http.StatusUnauthorized, "daemon 密钥无效，请重新运行安装命令")
		return
	}
	writeJSON(w, http.StatusOK, protocol.MeResponse{DaemonID: d.ID, Name: d.Name, UserID: d.UserID, Status: d.Status})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d, key, err := s.store.RegisterDaemon(r.Context(), strings.TrimSpace(req.Token), req.Hostname)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusUnauthorized, "安装令牌无效或已用过，请在页面上重新生成安装命令")
		return
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.log.Printf("daemon %s (%s) 注册成功", d.ID, d.Name)
	writeJSON(w, http.StatusOK, protocol.RegisterResponse{DaemonID: d.ID, Name: d.Name, Key: key})
}
