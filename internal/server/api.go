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

// actAsHeader:admin 令牌带上它，就按这个用户的身份执行(见 docs/v2/designs/embedding.md)。
const actAsHeader = "X-Agentuse-User"

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

// auth 要求登录：网页用 cookie,脚本用 Authorization: Bearer <用户令牌>。admin 可以再带 X-Agentuse-User 代为某个用户。
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
		if as := strings.TrimSpace(r.Header.Get(actAsHeader)); as != "" && as != u.ID {
			if !u.IsAdmin() {
				writeErr(w, http.StatusForbidden, "只有管理员令牌能带 "+actAsHeader)
				return
			}
			if u, err = s.actAs(r.Context(), as); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}

// actAs 取 X-Agentuse-User 指的用户，第一次出现就自动建(role = user,name 取 id)。
func (s *Server) actAs(ctx context.Context, id string) (store.User, error) {
	u, err := s.store.GetUser(ctx, id)
	if !errors.Is(err, store.ErrNotFound) {
		return u, err
	}
	if u, _, err = s.store.CreateUser(ctx, id, id, store.RoleUser); err != nil {
		if again, e := s.store.GetUser(ctx, id); e == nil { // 并发请求抢先建了
			return again, nil
		}
	}
	return u, err
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

func (s *Server) handleListHarnesses(w http.ResponseWriter, r *http.Request) {
	harnesses, err := s.store.ListHarnesses(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, harnesses)
}

func (s *Server) handleCreateHarness(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.store.CreateCustomHarness(r.Context(), currentUser(r).ID, req.Name, req.DisplayName, req.Description)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleDeleteHarness(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteCustomHarness(r.Context(), currentUser(r), r.PathValue("name")); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context(), currentUser(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Harness     string `json:"harness"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.store.CreateAgent(r.Context(), currentUser(r).ID, req.ID, req.Name, req.Harness, req.Description)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleUpdateAgent 改名字、换 harness、改说明;没给的字段不变。
func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        *string `json:"name"`
		Harness     *string `json:"harness"`
		Description *string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.store.UpdateAgent(r.Context(), currentUser(r), r.PathValue("id"),
		store.AgentPatch{Name: req.Name, Harness: req.Harness, Description: req.Description})
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAgent(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
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

func (s *Server) handleToggleHarness(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(r)
	id := r.PathValue("id")
	if err := s.store.SetDaemonHarnessEnabled(r.Context(), u, id, r.PathValue("harness"), req.Enabled); err != nil {
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

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.ListSessions(r.Context(), currentUser(r), 100)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

// messageContent 取请求里的消息内容:input 直接给 JSON,或者只给 prompt。
func messageContent(input json.RawMessage, prompt string) (json.RawMessage, error) {
	if len(input) > 0 {
		return input, nil
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt 或 input 必填")
	}
	return json.Marshal(protocol.PromptInput{Prompt: prompt})
}

// handleCreateSession 建会话并发出第一条消息。
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID string          `json:"agent_id"`
		Input   json.RawMessage `json:"input"`
		Prompt  string          `json:"prompt"`
		Cwd     string          `json:"cwd"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.AgentID == "" {
		writeErr(w, http.StatusBadRequest, "agent_id 必填")
		return
	}
	content, err := messageContent(req.Input, req.Prompt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	title := ""
	if len(req.Input) == 0 {
		title = firstRunes(strings.TrimSpace(req.Prompt), 50)
	}
	ss, m, err := s.store.CreateSession(r.Context(), currentUser(r), req.AgentID, strings.TrimSpace(req.Cwd), title, content)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.kickUser(ss.UserID)
	writeJSON(w, http.StatusCreated, map[string]any{"session": ss, "message": m})
}

// handleAddMessage 在会话里回复下一条消息。
func (s *Server) handleAddMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input  json.RawMessage `json:"input"`
		Prompt string          `json:"prompt"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	content, err := messageContent(req.Input, req.Prompt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ss, m, err := s.store.AddMessage(r.Context(), currentUser(r), r.PathValue("id"), content)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.kickUser(ss.UserID)
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	ss, err := s.store.GetSession(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	msgs, err := s.store.Messages(r.Context(), ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	events, err := s.store.SessionEvents(r.Context(), ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": ss, "messages": msgs, "events": events})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
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
