// Package controller serves authenticated queries and local harness debug actions.
// Queries are provided by the owning services through read-only interfaces.
package controller

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	runservice "github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/client/internal/service/session"

	"github.com/memory-co/agentuse/client/frontend"
)

// Queries expose service-owned file views without execution or mutation methods.
type SessionQueries interface {
	Conversations(context.Context, string) ([]session.Conversation, error)
	Sessions(context.Context, string, string) ([]session.Session, error)
	Session(context.Context, string, string, string) (session.Session, error)
}
type RunQueries interface {
	List(context.Context, runservice.Filter) (runservice.Page, error)
	Detail(context.Context, string, string, string, string) (runservice.Run, error)
	Stats(context.Context, string, runservice.Filter) ([]runservice.Statistic, error)
}

type Controller struct {
	sessionQueries SessionQueries
	runQueries     RunQueries
	token          string
	hosts          map[string]bool
	public         bool
	port           string
	mux            *http.ServeMux
	data           PublicData
}

func Listen(addr string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("监听地址必须是 IP 或 localhost")
	}
	network := "tcp4"
	if ip.To4() == nil {
		network = "tcp6"
	}
	return net.Listen(network, net.JoinHostPort(host, port))
}
func Token(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		s := strings.TrimSpace(string(b))
		if len(s) < 32 {
			return "", fmt.Errorf("本机页面令牌文件无效")
		}
		if err = os.Chmod(path, 0600); err != nil {
			return "", err
		}
		return s, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return Token(path)
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(token + "\n")
	return token, err
}

// PublicData supplies already-redacted configuration and runtime snapshots.
// Handlers never import execution services or read endpoint credentials.
type ReadFailure struct{ Err error }

type PublicData struct {
	SessionStatus  func(context.Context, string, string, string) (any, error)
	LocalCreate    func(context.Context, string) (session.Conversation, error)
	LocalStart     func(context.Context, string, json.RawMessage) (runservice.Run, error)
	LocalControl   func(context.Context, string, string, string, json.RawMessage) (any, error)
	LocalHarnesses func() (any, error)
	Endpoints      func() any
	Settings       func() any
	Harnesses      func() any
	Status         func() any
}

func New(sessions SessionQueries, runs RunQueries, addr, token string, data ...PublicData) (*Controller, error) {
	if len(token) < 32 {
		return nil, fmt.Errorf("本机页面令牌太短")
	}
	host, port, e := net.SplitHostPort(addr)
	if e != nil {
		return nil, e
	}
	ip := net.ParseIP(host)
	if host != "localhost" && ip == nil {
		return nil, fmt.Errorf("invalid listen address")
	}
	c := &Controller{public: ip != nil && ip.IsUnspecified(), port: port, sessionQueries: sessions, runQueries: runs, token: token, hosts: map[string]bool{net.JoinHostPort("127.0.0.1", port): true, net.JoinHostPort("localhost", port): true}, mux: http.NewServeMux()}
	c.hosts[net.JoinHostPort(host, port)] = true
	if len(data) > 0 {
		c.data = data[0]
	}
	c.registerAPI()
	assets := http.FileServerFS(frontend.FS())
	c.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiError(w, 404, "not_found", "接口不存在")
			return
		}
		assets.ServeHTTP(w, r)
	}))
	return c, nil
}
func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	allowed := c.hosts[strings.ToLower(r.Host)]
	if c.public {
		host, port, err := net.SplitHostPort(r.Host)
		allowed = err == nil && host != "" && port == c.port
	}
	if !allowed {
		apiError(w, 403, "forbidden", "invalid Host")
		return
	}
	localWrite := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/local/")
	if r.Method != http.MethodGet && !localWrite {
		w.Header().Set("Allow", "GET")
		apiError(w, 405, "method_not_allowed", "read-only endpoint")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		authorization := r.Header.Get("Authorization")
		credential := strings.TrimPrefix(authorization, "Bearer ")
		cookieName := "agentuse_client_" + c.port
		if authorization == "" {
			if cookie, err := r.Cookie(cookieName); err == nil {
				credential = cookie.Value
			}
		}
		if subtle.ConstantTimeCompare([]byte(credential), []byte(c.token)) != 1 {
			apiError(w, 401, "unauthorized", "请运行 agentuse ui 获取本机页面链接")
			return
		}

		if authorization != "" {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: c.token, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 30 * 24 * 60 * 60})
		}
	}
	if localWrite {
		content, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || content != "application/json" {
			apiError(w, 415, "unsupported_media_type", "需要 application/json")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if origin != scheme+"://"+r.Host {
				apiError(w, 403, "forbidden", "跨站请求不允许")
				return
			}
		}
	}
	c.mux.ServeHTTP(w, r)
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	jsonResponse(w, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func failure(w http.ResponseWriter, e error) {
	if errors.Is(e, os.ErrNotExist) {
		apiError(w, 404, "not_found", "记录不存在或已回收")
	} else if errors.Is(e, fileutil.ErrInvalid) {
		apiError(w, 400, "invalid_argument", "无效的目录标识")
	} else {
		apiError(w, 500, "read_failed", "本机文件读取失败")
	}
}
func (c *Controller) conversations(w http.ResponseWriter, r *http.Request) {
	ep := r.URL.Query().Get("endpoint_id")
	if !fileutil.Segment(ep) {
		apiError(w, 400, "invalid_argument", "endpoint_id required")
		return
	}
	v, e := c.sessionQueries.Conversations(r.Context(), ep)
	if e != nil {
		failure(w, e)
		return
	}
	respondPage(w, r, v, func(c session.Conversation) string { return c.ID })
}
func (c *Controller) conversation(w http.ResponseWriter, r *http.Request) {
	v, e := c.sessionQueries.Conversations(r.Context(), r.PathValue("endpoint"))
	if e != nil {
		failure(w, e)
		return
	}
	for _, c := range v {
		if c.ID == r.PathValue("conversation") {
			jsonResponse(w, c)
			return
		}
	}
	failure(w, os.ErrNotExist)
}
func (c *Controller) sessions(w http.ResponseWriter, r *http.Request) {
	v, e := c.sessionQueries.Sessions(r.Context(), r.PathValue("endpoint"), r.PathValue("conversation"))
	if e != nil {
		failure(w, e)
		return
	}
	filtered := []session.Session{}
	for _, s := range v {
		if h := r.URL.Query().Get("harness"); h != "" && h != s.Harness {
			continue
		}
		if status := r.URL.Query().Get("status"); status != "" && status != s.Status {
			continue
		}
		filtered = append(filtered, s)
	}
	respondPage(w, r, filtered, func(s session.Session) string { return s.ID })
}
func (c *Controller) session(w http.ResponseWriter, r *http.Request) {
	v, e := c.sessionQueries.Session(r.Context(), r.PathValue("endpoint"), r.PathValue("conversation"), r.PathValue("session"))
	if e != nil {
		failure(w, e)
		return
	}
	jsonResponse(w, v)
}
func queryFilter(r *http.Request) (runservice.Filter, error) {
	f := runservice.Filter{Endpoint: r.PathValue("endpoint"), Conversation: r.PathValue("conversation"), Session: r.PathValue("session"), Status: r.URL.Query().Get("status"), Limit: 50}
	for name, dst := range map[string]*int{"limit": &f.Limit, "cursor": &f.Offset} {
		if s := r.URL.Query().Get(name); s != "" {
			if name == "cursor" {
				decoded, e := decodeCursor(r, s)
				if e != nil {
					return f, e
				}
				s = decoded
			}
			v, e := strconv.Atoi(s)
			if e != nil || v < 0 {
				return f, fileutil.ErrInvalid
			}
			*dst = v
		}
	}
	if f.Limit < 1 || f.Limit > 200 {
		return f, fileutil.ErrInvalid
	}
	for name, dst := range map[string]*int64{"since": &f.After, "until": &f.Before} {
		if s := r.URL.Query().Get(name); s != "" {
			v, e := strconv.ParseInt(s, 10, 64)
			if e != nil || v < 0 {
				return f, fileutil.ErrInvalid
			}
			*dst = v
		}
	}
	return f, nil
}
func (c *Controller) list(w http.ResponseWriter, r *http.Request) {
	f, e := queryFilter(r)
	if e != nil {
		failure(w, e)
		return
	}
	if _, e = c.sessionQueries.Session(r.Context(), f.Endpoint, f.Conversation, f.Session); e != nil {
		failure(w, e)
		return
	}
	v, e := c.runQueries.List(r.Context(), f)
	if e != nil {
		failure(w, e)
		return
	}
	for i := range v.Runs {
		// Keep reliable conversation events inline; diagnostics are detail-only.
		events := v.Runs[i].Events[:0]
		for _, event := range v.Runs[i].Events {
			if event.Kind != "log" {
				events = append(events, event)
			}
		}
		v.Runs[i].Events = events
	}
	if v.NextCursor != "" {
		v.NextCursor = encodeCursor(r, v.NextCursor)
	}
	jsonResponse(w, v)
}
func (c *Controller) detail(w http.ResponseWriter, r *http.Request) {
	v, e := c.runQueries.Detail(r.Context(), r.PathValue("endpoint"), r.PathValue("conversation"), r.PathValue("session"), r.PathValue("run"))
	if e != nil {
		failure(w, e)
		return
	}
	jsonResponse(w, v)
}
func (c *Controller) stats(w http.ResponseWriter, r *http.Request) {
	f, e := queryFilter(r)
	if e != nil {
		failure(w, e)
		return
	}
	v, e := c.runQueries.Stats(r.Context(), "harness", f)
	if e != nil {
		failure(w, e)
		return
	}
	jsonResponse(w, map[string]any{"items": v, "scope": "retained_session_runs"})
}
func respondPage[T any](w http.ResponseWriter, r *http.Request, items []T, key func(T) string) {
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 || v > 200 {
			failure(w, fileutil.ErrInvalid)
			return
		}
		limit = v
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		var e error
		cursor, e = decodeCursor(r, cursor)
		if e != nil {
			failure(w, e)
			return
		}
	}
	start := 0
	for start < len(items) && key(items[start]) <= cursor && cursor != "" {
		start++
	}
	end := min(len(items), start+limit)
	next := ""
	if end < len(items) && end > start {
		next = encodeCursor(r, key(items[end-1]))
	}
	jsonResponse(w, map[string]any{"items": items[start:end], "next_cursor": next})
}

func cursorScope(r *http.Request) string {
	q := r.URL.Query()
	q.Del("cursor")
	return r.URL.EscapedPath() + "?" + q.Encode()
}
func encodeCursor(r *http.Request, position string) string {
	b, _ := json.Marshal([]string{cursorScope(r), position})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCursor(r *http.Request, s string) (string, error) {
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil {
		return "", fileutil.ErrInvalid
	}
	var values []string
	if json.Unmarshal(b, &values) != nil || len(values) != 2 || values[0] != cursorScope(r) {
		return "", fileutil.ErrInvalid
	}
	return values[1], nil
}

func localBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 300<<10)
	b, e := io.ReadAll(r.Body)
	if e != nil || !json.Valid(b) {
		return nil, fileutil.ErrInvalid
	}
	return b, nil
}
func (c *Controller) localHarnesses(w http.ResponseWriter, r *http.Request) {
	if c.data.LocalHarnesses == nil {
		apiError(w, 503, "unavailable", "本地执行服务尚未就绪")
		return
	}
	v, e := c.data.LocalHarnesses()
	if e != nil {
		apiError(w, 400, "harness_error", e.Error())
		return
	}
	jsonResponse(w, map[string]any{"items": v})
}
func (c *Controller) localCreate(w http.ResponseWriter, r *http.Request) {
	if c.data.LocalCreate == nil {
		apiError(w, 503, "unavailable", "本地执行服务尚未就绪")
		return
	}
	b, e := localBody(w, r)
	var v createConversationBody
	if e != nil || json.Unmarshal(b, &v) != nil {
		failure(w, fileutil.ErrInvalid)
		return
	}
	result, e := c.data.LocalCreate(r.Context(), v.Title)
	if e != nil {
		apiError(w, 400, "local_error", e.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	jsonResponse(w, result)
}
func (c *Controller) localStart(w http.ResponseWriter, r *http.Request) {
	if c.data.LocalStart == nil {
		apiError(w, 503, "unavailable", "本地执行服务尚未就绪")
		return
	}
	b, e := localBody(w, r)
	if e != nil {
		failure(w, e)
		return
	}
	result, e := c.data.LocalStart(r.Context(), r.PathValue("conversation"), b)
	if e != nil {
		apiError(w, 400, "local_error", e.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	jsonResponse(w, result)
}
func (c *Controller) localControl(w http.ResponseWriter, r *http.Request) {
	if c.data.LocalControl == nil {
		apiError(w, 503, "unavailable", "本地执行服务尚未就绪")
		return
	}
	b, e := localBody(w, r)
	if e != nil {
		failure(w, e)
		return
	}
	result, e := c.data.LocalControl(r.Context(), r.PathValue("conversation"), r.PathValue("session"), r.PathValue("action"), b)
	if e != nil {
		apiError(w, 400, "local_error", e.Error())
		return
	}
	jsonResponse(w, result)
}

func (c *Controller) sessionStatus(w http.ResponseWriter, r *http.Request) {
	if c.data.SessionStatus == nil {
		apiError(w, 503, "unavailable", "session runtime is unavailable")
		return
	}
	state, err := c.data.SessionStatus(r.Context(), r.PathValue("endpoint"), r.PathValue("conversation"), r.PathValue("session"))
	if err != nil {
		failure(w, err)
		return
	}
	jsonResponse(w, state)
}
