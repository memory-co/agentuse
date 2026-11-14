package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

const cookieName = "agentuse_token"

// actAsHeader:admin 令牌带上它，就按这个用户的身份执行(见 designs/v3/server-user.md)。
const actAsHeader = "X-Agentuse-User"

type userKey struct{}

func currentUser(r *http.Request) service.User { return r.Context().Value(userKey{}).(service.User) }

func requestToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}

// auth accepts a human user cookie/token; device execution has its own verifier.
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := requestToken(r)
		if token == "" {
			writeErr(w, 401, "未登录")
			return
		}
		u, err := s.service.UserService.UserByToken(r.Context(), token)
		if err != nil {
			writeErr(w, 401, "令牌无效")
			return
		}
		if as := strings.TrimSpace(r.Header.Get(actAsHeader)); as != "" && as != u.ID {
			if !u.IsAdmin() {
				writeErr(w, 403, "只有管理员可代为用户")
				return
			}
			u, err = s.actAs(r.Context(), as)
			if err != nil {
				s.storeErr(w, err)
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}

// actAs 取 X-Agentuse-User 指的用户，第一次出现就自动建(role = user,name 取 id)。
func (s *Server) actAs(ctx context.Context, id string) (service.User, error) {
	u, err := s.service.UserService.GetUser(ctx, id)
	if !errors.Is(err, service.ErrNotFound) {
		return u, err
	}
	if u, _, err = s.service.UserService.CreateUser(ctx, id, id, service.RoleUser); err != nil {
		if again, e := s.service.UserService.GetUser(ctx, id); e == nil { // 并发请求抢先建了
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
	if _, err := s.service.UserService.UserByToken(r.Context(), tok); err != nil {
		http.Redirect(w, r, "/?login=invalid", http.StatusFound)
		return
	}
	s.setCookie(w, r, tok, 365*24*3600)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginBody
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.service.UserService.UserByToken(r.Context(), strings.TrimSpace(req.Token))
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
	users, err := s.service.UserService.ListUsers(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req agentuse.CreateUserRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Role == "" {
		req.Role = service.RoleUser
	}
	u, token, err := s.service.UserService.CreateUser(r.Context(), req.ID, req.Name, req.Role)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentuse.UserToken{User: u, Token: token, LoginURL: s.baseURL(r) + "/login?token=" + token})
}

func (s *Server) handleResetUserToken(w http.ResponseWriter, r *http.Request) {
	u, token, err := s.service.UserService.ResetUserToken(r.Context(), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentuse.UserToken{User: u, Token: token, LoginURL: s.baseURL(r) + "/login?token=" + token})
}

func (s *Server) handleListHarnesses(w http.ResponseWriter, r *http.Request) {
	harnesses, err := s.service.DeviceService.ListHarnesses(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, harnesses)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if source := currentSource(r); source != nil {
		v, err := s.service.ConversationService.GetConversation(r.Context(), source.User, source.ConversationID)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		if v.ParentConversationID != "" || v.Mode != agentuse.ConversationModeCollaborative {
			writeErr(w, 403, "agent selection is only available to the top-level coordinator")
			return
		}
	}
	agents, err := s.service.AgentService.ListAgents(r.Context(), currentUser(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req agentuse.CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.service.AgentService.CreateConfiguredAgent(r.Context(), currentUser(r).ID, req.ID, agentuse.AgentConfig{Name: req.Name, DeviceID: req.DeviceID, Harness: req.Harness, Description: req.Description, Model: req.Model, Instructions: req.Instructions, Skills: req.Skills})
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.service.AgentService.GetAgent(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.service.AgentService.DeleteAgent(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deviceView 是页面上一台 device:待注册的附带安装命令。
type deviceView struct {
	service.Device
	SetupCommand string `json:"setup_command,omitempty"`
}

func (s *Server) view(r *http.Request, d service.Device) deviceView {
	v := deviceView{Device: d}
	if d.SetupToken != "" {
		v.SetupCommand = setupCommand(s.baseURL(r), d.SetupToken)
	}
	return v
}

func setupCommand(base, token string) string {
	return `curl -fsSL "` + base + `/setup.sh" -o setup.sh && TOKEN="` + token + `" bash setup.sh`
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.service.DeviceService.ListDevices(r.Context(), currentUser(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := make([]deviceView, 0, len(ds))
	for _, d := range ds {
		out = append(out, s.view(r, d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var req createDeviceBody
	if r.ContentLength != 0 {
		if err := readJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	d, err := s.service.DeviceService.CreateDevice(r.Context(), currentUser(r).ID, req.Name)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.view(r, d))
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.service.DeviceService.DeleteDevice(r.Context(), currentUser(r), id); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetDevice(w http.ResponseWriter, r *http.Request) {
	d, err := s.service.DeviceService.ResetDeviceSetup(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(r, d))
}

func (s *Server) handleToggleHarness(w http.ResponseWriter, r *http.Request) {
	var req toggleHarnessBody
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(r)
	id := r.PathValue("id")
	if err := s.service.DeviceService.SetDeviceHarnessEnabled(r.Context(), u, id, r.PathValue("harness"), req.Enabled); err != nil {
		s.storeErr(w, err)
		return
	}
	d, err := s.service.DeviceService.GetDevice(r.Context(), u, id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(r, d))
}

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	for key := range r.URL.Query() {
		if key != "parent_conversation_id" {
			writeErr(w, 400, "unknown query parameter: "+key)
			return
		}
	}
	parent := r.URL.Query().Get("parent_conversation_id")
	if source := currentSource(r); source != nil {
		own, err := s.service.ConversationService.GetConversation(r.Context(), source.User, source.ConversationID)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		if own.ParentConversationID != "" || own.Mode != agentuse.ConversationModeCollaborative || parent != own.ID {
			writeErr(w, 403, "only a top-level execution may list its direct children")
			return
		}
	}
	var out []service.Conversation
	var err error
	if parent != "" {
		out, err = s.service.ConversationService.ListChildren(r.Context(), currentUser(r), parent, 100)
	} else {
		out, err = s.service.ConversationService.ListConversations(r.Context(), currentUser(r), 100)
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req createConversationBody
	if err := readMessageJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	v, reply, err := s.service.ConversationService.CreateConversation(r.Context(), currentUser(r), req.ParentConversationID, req.Title, req.Prompt, req.ToAgentIDs, req.ToUserIDs, req.Mode, currentSource(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := agentuse.CreateConversationResponse{Conversation: v, Runs: []agentuse.Run{}, Deliveries: []agentuse.Delivery{}}
	if reply != nil {
		out.Round = reply.Round
		out.Message = reply.Message
		out.Runs = reply.Runs
		out.Deliveries = reply.Deliveries
	}
	writeJSON(w, 201, out)
}
func (s *Server) handleAddMessage(w http.ResponseWriter, r *http.Request) {
	var req messageBody
	if err := readMessageJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var reply service.Reply
	var err error
	if source := currentSource(r); source != nil {
		reply, err = s.service.ConversationService.SendAgentMessage(r.Context(), *source, r.PathValue("id"), req.Prompt, req.ToAgentIDs, req.ToUserIDs, req.AttachmentIDs, req.ReplyToMessageID)
	} else {
		reply, err = s.service.ConversationService.SendMessage(r.Context(), currentUser(r), r.PathValue("id"), req.Prompt, req.ToAgentIDs, req.ToUserIDs, req.AttachmentIDs, req.ReplyToMessageID)
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 201, reply)
}
func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	if !s.conversationInScope(w, r, r.PathValue("id"), false) {
		return
	}
	for key := range r.URL.Query() {
		if key != "round" {
			writeErr(w, 400, "unknown query parameter: "+key)
			return
		}
	}
	var v service.ConversationDetail
	var err error
	if raw, ok := r.URL.Query()["round"]; ok {
		if len(raw) != 1 {
			writeErr(w, 400, "round must be a positive integer")
			return
		}
		seq, e := strconv.ParseInt(raw[0], 10, 64)
		if e != nil || seq <= 0 {
			writeErr(w, 400, "round must be a positive integer")
			return
		}
		v, err = s.service.ConversationService.ReadRound(r.Context(), currentUser(r), r.PathValue("id"), seq)
	} else {
		v, err = s.service.ConversationService.ReadConversation(r.Context(), currentUser(r), r.PathValue("id"))
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) conversationInScope(w http.ResponseWriter, r *http.Request, id string, write bool) bool {
	if source := currentSource(r); source != nil {
		if err := s.service.ConversationService.CheckAgentAccess(r.Context(), *source, id, write); err != nil {
			s.storeErr(w, err)
			return false
		}
	}
	return true
}
func (s *Server) handleResolveConversation(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.ConversationService.ResolveConversation(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleReopenConversation(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.ConversationService.ReopenConversation(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	recursive := r.URL.Query().Get("recursive")
	if recursive != "" && recursive != "true" && recursive != "false" {
		writeErr(w, 400, "recursive must be true or false")
		return
	}
	var err error
	if recursive == "true" {
		err = s.service.ConversationService.DeleteTree(r.Context(), currentUser(r), r.PathValue("id"))
	} else {
		err = s.service.ConversationService.DeleteConversation(r.Context(), currentUser(r), r.PathValue("id"))
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// handleDeviceMe:device 用密钥查自己，验证密钥是否还有效。
func (s *Server) handleDeviceMe(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := s.service.DeviceService.DeviceByKey(r.Context(), key)
	if key == "" || err != nil {
		writeErr(w, http.StatusUnauthorized, "device 密钥无效，请重新运行安装命令")
		return
	}
	writeJSON(w, http.StatusOK, protocol.MeResponse{DeviceID: d.ID, Name: d.Name, UserID: d.UserID, Status: d.Status})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d, key, err := s.service.DeviceService.RegisterDevice(r.Context(), strings.TrimSpace(req.Token), req.Hostname)
	if errors.Is(err, service.ErrNotFound) {
		writeErr(w, http.StatusUnauthorized, "安装令牌无效或已用过，请在页面上重新生成安装命令")
		return
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.log.Printf("device %s (%s) 注册成功", d.ID, d.Name)
	writeJSON(w, http.StatusOK, protocol.RegisterResponse{DeviceID: d.ID, Name: d.Name, Key: key})
}
