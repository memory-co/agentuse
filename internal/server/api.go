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

// workKey:请求用的是工作令牌(wt_)时，放着它能动的那个工作。
type workKey struct{}

func currentUser(r *http.Request) store.User { return r.Context().Value(userKey{}).(store.User) }

// workScope 是工作令牌限定的工作;用户令牌时返回 nil。
func workScope(r *http.Request) *store.Work {
	w, _ := r.Context().Value(workKey{}).(*store.Work)
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

// workAuth 同 auth,另外收工作令牌(wt_):协调者跑的时候用它开线程、看线程(docs/v2/apis/works.md)。
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
			wk, u, err := s.store.WorkByToken(r.Context(), tok)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "工作令牌无效或已过期(只在协调者这条消息还在跑时有效)")
				return
			}
			ctx := context.WithValue(r.Context(), userKey{}, u)
			h(w, r.WithContext(context.WithValue(ctx, workKey{}, &wk)))
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
		DaemonID    string `json:"daemon_id"`
		Harness     string `json:"harness"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.store.CreateAgent(r.Context(), currentUser(r).ID, req.ID, req.Name, req.DaemonID, req.Harness, req.Description)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetAgent(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleUpdateAgent 改名字、切换运行时(daemon_id + harness 一起给)、改说明;没给的字段不变。
func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        *string `json:"name"`
		DaemonID    *string `json:"daemon_id"`
		Harness     *string `json:"harness"`
		Description *string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.store.UpdateAgent(r.Context(), currentUser(r), r.PathValue("id"),
		store.AgentPatch{Name: req.Name, DaemonID: req.DaemonID, Harness: req.Harness, Description: req.Description})
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

// handleListSessions:不带 work_id 列普通会话，带了列那个工作的线程。工作令牌只能列自己工作的(不带就是它)。
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
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
	var sessions []store.Session
	var err error
	if workID != "" {
		sessions, err = s.store.ListWorkSessions(r.Context(), currentUser(r), workID, 100)
	} else {
		sessions, err = s.store.ListSessions(r.Context(), currentUser(r), 100)
	}
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
		Mode           string          `json:"mode"`
		Title          string          `json:"title"`
		TargetAgentIDs []string        `json:"target_agent_ids"`
		DeferStart     bool            `json:"defer_start"`
		AgentID        string          `json:"agent_id"`
		WorkID         string          `json:"work_id"`
		Input          json.RawMessage `json:"input"`
		Prompt         *string         `json:"prompt"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Mode != "" && req.Mode != store.MultiAgent {
		writeErr(w, 400, "只支持 multi_agent session")
		return
	}
	if req.DeferStart && (req.Prompt != nil || len(req.Input) > 0 || len(req.TargetAgentIDs) > 0) {
		writeErr(w, 400, "defer_start 不能同时发送任务")
		return
	}
	s.createMultiSession(w, r, req.WorkID, req.Title, req.AgentID, req.Input, req.Prompt, req.TargetAgentIDs)
}

func (s *Server) handleAddMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetAgentIDs []string        `json:"target_agent_ids"`
		Input          json.RawMessage `json:"input"`
		Prompt         string          `json:"prompt"`
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
	u, id := currentUser(r), r.PathValue("id")
	if !s.threadInScope(w, r, id) {
		return
	}
	reply, err := s.store.SendMultiMessage(r.Context(), u, id, content, req.TargetAgentIDs, r.Header.Get("Idempotency-Key"), len(req.Input) == 0)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.kickAll()
	s.hub.pushRunSteers(reply.Deliveries)
	writeJSON(w, 201, reply)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	ss, err := s.store.GetSession(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.getMultiSession(w, r, ss)
}

func (s *Server) threadInScope(w http.ResponseWriter, r *http.Request, id string) bool {
	sc := workScope(r)
	if sc == nil {
		return true
	}
	ss, err := s.store.GetSession(r.Context(), currentUser(r), id)
	if err != nil {
		s.storeErr(w, err)
		return false
	}
	if ss.WorkID != sc.ID || (ss.WorkRole != store.WorkThread && ss.Mode != store.MultiAgent) {
		writeErr(w, http.StatusForbidden, "工作令牌只能动自己工作的线程")
		return false
	}
	return true
}

// handleResolveSession 标记已完成;handleReopenSession 取消已完成。
func (s *Server) handleResolveSession(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	ss, err := s.store.ResolveSession(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ss)
}

func (s *Server) handleReopenSession(w http.ResponseWriter, r *http.Request) {
	ss, err := s.store.ReopenSession(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ss)
}

func (s *Server) handleListWorks(w http.ResponseWriter, r *http.Request) {
	works, err := s.store.ListWorks(r.Context(), currentUser(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, works)
}

// handleCreateWork 建工作，同时建出协调者会话。
func (s *Server) handleCreateWork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode         string `json:"mode"`
		Name         string `json:"name"`
		AgentID      string `json:"agent_id"`
		Instructions string `json:"instructions"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Mode != "" && req.Mode != store.MultiAgent {
		writeErr(w, 400, "只支持 multi_agent session")
		return
	}
	create := s.store.CreateMultiWork
	wk, coord, err := create(r.Context(), currentUser(r), req.Name, req.AgentID, req.Instructions)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"work": wk, "coordinator": coord})
}

func (s *Server) handleGetWork(w http.ResponseWriter, r *http.Request) {
	wk, err := s.store.GetWork(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wk)
}

func (s *Server) handleUpdateWork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CoordinatorAgentID *string `json:"coordinator_agent_id"`
		Name               *string `json:"name"`
		Instructions       *string `json:"instructions"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	wk, err := s.store.UpdateWork(r.Context(), currentUser(r), r.PathValue("id"), store.WorkPatch{Name: req.Name, Instructions: req.Instructions, CoordinatorAgentID: req.CoordinatorAgentID})
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wk)
}

func (s *Server) handleDeleteWork(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteWork(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
