// Package controller serves the token-protected read-only UI.
// It imports store.Reader, never the execution service or its Writer.
package controller

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/memory-co/agentuse/client/frontend"
	"github.com/memory-co/agentuse/client/internal/store"
)

type Controller struct {
	reader store.Reader
	token  string
	hosts  map[string]bool
	public bool
	port   string
	mux    *http.ServeMux
	data   PublicData
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
	Endpoints func() any
	Settings  func() any
	Harnesses func() any
	Status    func() any
}

func New(reader store.Reader, addr, token string, data ...PublicData) (*Controller, error) {
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
	c := &Controller{public: ip != nil && ip.IsUnspecified(), port: port, reader: reader, token: token, hosts: map[string]bool{net.JoinHostPort("127.0.0.1", port): true, net.JoinHostPort("localhost", port): true}, mux: http.NewServeMux()}
	c.hosts[net.JoinHostPort(host, port)] = true
	if len(data) > 0 {
		c.data = data[0]
	}
	for _, entry := range []struct {
		path string
		fn   func() any
	}{{"/api/endpoints", c.data.Endpoints}, {"/api/settings", c.data.Settings}, {"/api/harnesses", c.data.Harnesses}, {"/api/status", c.data.Status}} {
		fn := entry.fn
		c.mux.HandleFunc("GET "+entry.path, func(w http.ResponseWriter, r *http.Request) {
			if fn == nil {
				jsonResponse(w, map[string]any{})
				return
			}
			value := fn()
			if failed, ok := value.(ReadFailure); ok {
				failure(w, failed.Err)
				return
			}
			jsonResponse(w, value)
		})
	}
	c.mux.HandleFunc("GET /api/conversations", c.conversations)
	base := "/api/conversations/{endpoint}/{conversation}"
	c.mux.HandleFunc("GET "+base, c.conversation)
	c.mux.HandleFunc("GET "+base+"/sessions", c.sessions)
	c.mux.HandleFunc("GET "+base+"/sessions/{session}", c.session)
	c.mux.HandleFunc("GET "+base+"/sessions/{session}/runs", c.list)
	c.mux.HandleFunc("GET "+base+"/sessions/{session}/runs/{run}/attempts/{attempt}", c.detail)
	c.mux.HandleFunc("GET "+base+"/sessions/{session}/usage", c.stats)
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
	if r.Method != http.MethodGet {
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
	} else if errors.Is(e, store.ErrInvalid) {
		apiError(w, 400, "invalid_argument", "无效的目录标识")
	} else {
		apiError(w, 500, "read_failed", "本机文件读取失败")
	}
}
func (c *Controller) conversations(w http.ResponseWriter, r *http.Request) {
	ep := r.URL.Query().Get("endpoint_id")
	if !store.Segment(ep) {
		apiError(w, 400, "invalid_argument", "endpoint_id required")
		return
	}
	v, e := c.reader.Conversations(r.Context(), ep)
	if e != nil {
		failure(w, e)
		return
	}
	respondPage(w, r, v, func(c store.Conversation) string { return c.ID })
}
func (c *Controller) conversation(w http.ResponseWriter, r *http.Request) {
	v, e := c.reader.Conversations(r.Context(), r.PathValue("endpoint"))
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
	v, e := c.reader.Sessions(r.Context(), r.PathValue("endpoint"), r.PathValue("conversation"))
	if e != nil {
		failure(w, e)
		return
	}
	filtered := []store.Session{}
	for _, s := range v {
		if h := r.URL.Query().Get("harness"); h != "" && h != s.Harness {
			continue
		}
		if status := r.URL.Query().Get("status"); status != "" && status != s.Status {
			continue
		}
		filtered = append(filtered, s)
	}
	respondPage(w, r, filtered, func(s store.Session) string { return s.ID })
}
func (c *Controller) session(w http.ResponseWriter, r *http.Request) {
	v, e := c.reader.Session(r.Context(), r.PathValue("endpoint"), r.PathValue("conversation"), r.PathValue("session"))
	if e != nil {
		failure(w, e)
		return
	}
	jsonResponse(w, v)
}
func queryFilter(r *http.Request) (store.Filter, error) {
	f := store.Filter{Endpoint: r.PathValue("endpoint"), Conversation: r.PathValue("conversation"), Session: r.PathValue("session"), Status: r.URL.Query().Get("status"), Limit: 50}
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
				return f, store.ErrInvalid
			}
			*dst = v
		}
	}
	if f.Limit < 1 || f.Limit > 200 {
		return f, store.ErrInvalid
	}
	for name, dst := range map[string]*int64{"since": &f.After, "until": &f.Before} {
		if s := r.URL.Query().Get(name); s != "" {
			v, e := strconv.ParseInt(s, 10, 64)
			if e != nil || v < 0 {
				return f, store.ErrInvalid
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
	if _, e = c.reader.Session(r.Context(), f.Endpoint, f.Conversation, f.Session); e != nil {
		failure(w, e)
		return
	}
	v, e := c.reader.List(r.Context(), f)
	if e != nil {
		failure(w, e)
		return
	}
	for i := range v.Runs {
		v.Runs[i].Events = nil
		v.Runs[i].Output = ""
	}
	if v.NextCursor != "" {
		v.NextCursor = encodeCursor(r, v.NextCursor)
	}
	jsonResponse(w, v)
}
func (c *Controller) detail(w http.ResponseWriter, r *http.Request) {
	v, e := c.reader.Detail(r.Context(), r.PathValue("endpoint"), r.PathValue("conversation"), r.PathValue("session"), r.PathValue("run"), r.PathValue("attempt"))
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
	v, e := c.reader.Stats(r.Context(), "harness", f)
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
			failure(w, store.ErrInvalid)
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
		return "", store.ErrInvalid
	}
	var values []string
	if json.Unmarshal(b, &values) != nil || len(values) != 2 || values[0] != cursorScope(r) {
		return "", store.ErrInvalid
	}
	return values[1], nil
}
