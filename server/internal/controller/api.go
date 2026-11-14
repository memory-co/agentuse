package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

const cookieName = "agentuse_token"

// actAsHeader:admin 令牌带上它，就按这个用户的身份执行(见 designs/v3/server-user.md)。
const actAsHeader = "X-Agentuse-User"

type userKey struct{}

// workKey holds the Server-authorized Work scope, from a tool credential or management route.
type workKey struct{}

func currentUser(r *http.Request) service.User { return r.Context().Value(userKey{}).(service.User) }

// workScope is derived by the Server; ordinary conversation requests have no Work scope.
func workScope(r *http.Request) *service.Work {
	w, _ := r.Context().Value(workKey{}).(*service.Work)
	return w
}

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
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc { return s.authWith(h, false) }

// workAuth 同 auth,另外收工作令牌(wt_):协调者跑的时候用它开线程、看线程(designs/v3/server-work.md)。
// 用工作令牌时身份是工作的主人，handler 里再用 workScope 限定只能动这个工作的线程。
func (s *Server) workAuth(h http.HandlerFunc) http.HandlerFunc { return s.authWith(h, true) }

func (s *Server) authWith(h http.HandlerFunc, allowWork bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := requestToken(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "未登录")
			return
		}
		if strings.HasPrefix(tok, "wt_") {
			if !allowWork {
				writeErr(w, http.StatusForbidden, "工作令牌不能调这个接口")
				return
			}
			wk, u, err := s.service.WorkByToken(r.Context(), tok)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "工作令牌无效或已过期(只在协调者这条消息还在跑时有效)")
				return
			}
			ctx := context.WithValue(r.Context(), userKey{}, u)
			h(w, r.WithContext(context.WithValue(ctx, workKey{}, &wk)))
			return
		}
		u, err := s.service.UserByToken(r.Context(), tok)
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
func (s *Server) actAs(ctx context.Context, id string) (service.User, error) {
	u, err := s.service.GetUser(ctx, id)
	if !errors.Is(err, service.ErrNotFound) {
		return u, err
	}
	if u, _, err = s.service.CreateUser(ctx, id, id, service.RoleUser); err != nil {
		if again, e := s.service.GetUser(ctx, id); e == nil { // 并发请求抢先建了
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
	if _, err := s.service.UserByToken(r.Context(), tok); err != nil {
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
	u, err := s.service.UserByToken(r.Context(), strings.TrimSpace(req.Token))
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
	users, err := s.service.ListUsers(r.Context())
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
	u, token, err := s.service.CreateUser(r.Context(), req.ID, req.Name, req.Role)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentuse.UserToken{User: u, Token: token, LoginURL: s.baseURL(r) + "/login?token=" + token})
}

func (s *Server) handleResetUserToken(w http.ResponseWriter, r *http.Request) {
	u, token, err := s.service.ResetUserToken(r.Context(), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentuse.UserToken{User: u, Token: token, LoginURL: s.baseURL(r) + "/login?token=" + token})
}

func (s *Server) handleListHarnesses(w http.ResponseWriter, r *http.Request) {
	harnesses, err := s.service.ListHarnesses(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, harnesses)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.service.ListAgents(r.Context(), currentUser(r))
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
	a, err := s.service.CreateConfiguredAgent(r.Context(), currentUser(r).ID, req.ID, agentuse.AgentConfig{Name: req.Name, DeviceID: req.DeviceID, Harness: req.Harness, Description: req.Description, Model: req.Model, Instructions: req.Instructions, Skills: req.Skills})
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.service.GetAgent(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeleteAgent(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
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
	ds, err := s.service.ListDevices(r.Context(), currentUser(r))
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
	d, err := s.service.CreateDevice(r.Context(), currentUser(r).ID, req.Name)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.view(r, d))
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.service.DeleteDevice(r.Context(), currentUser(r), id); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetDevice(w http.ResponseWriter, r *http.Request) {
	d, err := s.service.ResetDeviceSetup(r.Context(), currentUser(r), r.PathValue("id"))
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
	if err := s.service.SetDeviceHarnessEnabled(r.Context(), u, id, r.PathValue("harness"), req.Enabled); err != nil {
		s.storeErr(w, err)
		return
	}
	d, err := s.service.GetDevice(r.Context(), u, id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(r, d))
}

// handleListConversations:不带 work_id 列普通会话，带了列那个工作的线程。工作令牌只能列自己工作的(不带就是它)。
func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	workID := r.URL.Query().Get("work_id")
	if sc := workScope(r); sc != nil {
		if workID == "" {
			workID = sc.ID
		}
		if workID != sc.ID {
			writeErr(w, http.StatusForbidden, "工作令牌只能看自己工作的线程")
			return
		}
	}
	var conversations []service.Conversation
	var err error
	if workID != "" {
		conversations, err = s.service.ListWorkConversations(r.Context(), currentUser(r), workID, 100)
	} else {
		conversations, err = s.service.ListConversations(r.Context(), currentUser(r), 100)
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversations)
}

// handleCreateConversation 建会话并发出第一条消息。
func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req createConversationBody
	if err := readMessageJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.createConversation(w, r, req.WorkID, req.Title, req.Prompt, req.ToAgentIDs, req.ToUserIDs)
}

func (s *Server) handleAddMessage(w http.ResponseWriter, r *http.Request) {
	var req messageBody
	if err := readMessageJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, id := currentUser(r), r.PathValue("id")
	if !s.threadInScope(w, r, id) {
		return
	}
	reply, err := s.service.SendMessage(r.Context(), u, id, req.Prompt, req.ToAgentIDs, req.ToUserIDs, req.AttachmentIDs, req.ReplyToMessageID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 201, reply)
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	ss, err := s.service.GetConversation(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.getConversation(w, r, ss)
}

func (s *Server) threadInScope(w http.ResponseWriter, r *http.Request, id string) bool {
	sc := workScope(r)
	if sc == nil {
		return true
	}
	ss, err := s.service.GetConversation(r.Context(), currentUser(r), id)
	if err != nil {
		s.storeErr(w, err)
		return false
	}
	if ss.WorkID != sc.ID || ss.WorkRole != service.WorkThread {
		writeErr(w, http.StatusForbidden, "只能操作当前工作内的线程")
		return false
	}
	return true
}

// handleResolveConversation 标记已完成;handleReopenConversation 取消已完成。
func (s *Server) handleResolveConversation(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	ss, err := s.service.ResolveConversation(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ss)
}

func (s *Server) handleReopenConversation(w http.ResponseWriter, r *http.Request) {
	ss, err := s.service.ReopenConversation(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ss)
}

func (s *Server) handleListWorks(w http.ResponseWriter, r *http.Request) {
	works, err := s.service.ListWorks(r.Context(), currentUser(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, works)
}

// handleCreateWork 建工作，同时建出协调者会话。
func (s *Server) handleCreateWork(w http.ResponseWriter, r *http.Request) {
	var req agentuse.CreateWorkRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	create := s.service.CreateWork
	wk, coord, err := create(r.Context(), currentUser(r), req.Name, req.AgentID, req.Instructions)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentuse.CreateWorkResponse{Work: wk, Coordinator: coord})
}

func (s *Server) handleGetWork(w http.ResponseWriter, r *http.Request) {
	wk, err := s.service.GetWork(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wk)
}

func (s *Server) handleUpdateWork(w http.ResponseWriter, r *http.Request) {
	var req agentuse.WorkPatch
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	wk, err := s.service.UpdateWork(r.Context(), currentUser(r), r.PathValue("id"), service.WorkPatch{Name: req.Name, Instructions: req.Instructions, CoordinatorAgentID: req.CoordinatorAgentID})
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wk)
}

func (s *Server) handleDeleteWork(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeleteWork(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeleteConversation(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
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

// handleDeviceMe:device 用密钥查自己，验证密钥是否还有效。
func (s *Server) handleDeviceMe(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := s.service.DeviceByKey(r.Context(), key)
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
	d, key, err := s.service.RegisterDevice(r.Context(), strings.TrimSpace(req.Token), req.Hostname)
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
