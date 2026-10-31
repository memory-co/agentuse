// Package controller serves the loopback-only, token-protected read-only UI.
// It imports store.Reader, never the execution service or its Writer.
package controller

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/memory-co/agentuse/client/frontend"
	"github.com/memory-co/agentuse/client/internal/store"
)

type Controller struct {
	reader store.Reader
	token  string
	hosts  map[string]bool
	mux    *http.ServeMux
}

func Listen(addr string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host != "127.0.0.1" && host != "localhost" {
		return nil, fmt.Errorf("本机页面只能监听 127.0.0.1 或 localhost")
	}
	return net.Listen("tcp4", net.JoinHostPort("127.0.0.1", port))
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
func New(reader store.Reader, addr, token string) (*Controller, error) {
	if len(token) < 32 {
		return nil, fmt.Errorf("本机页面令牌太短")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host != "127.0.0.1" && host != "localhost" {
		return nil, fmt.Errorf("本机页面只允许回环地址")
	}
	c := &Controller{reader: reader, token: token, hosts: map[string]bool{net.JoinHostPort("127.0.0.1", port): true, net.JoinHostPort("localhost", port): true}, mux: http.NewServeMux()}
	c.mux.HandleFunc("GET /api/runs", c.list)
	c.mux.HandleFunc("GET /api/runs/{run}/{attempt}", c.detail)
	c.mux.HandleFunc("GET /api/stats", c.stats)
	c.mux.Handle("GET /", http.FileServerFS(frontend.FS()))
	return c, nil
}
func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !c.hosts[strings.ToLower(r.Host)] {
		http.Error(w, "invalid Host", 403)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "read-only endpoint", 405)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	equal := func(s string) bool { return subtle.ConstantTimeCompare([]byte(s), []byte(c.token)) == 1 }
	if r.URL.Path == "/" && equal(r.URL.Query().Get("token")) {
		http.SetCookie(w, &http.Cookie{Name: "agentuse_local", Value: c.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
		u := *r.URL
		q := u.Query()
		q.Del("token")
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.String(), http.StatusSeeOther)
		return
	}
	credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if credential == "" {
		if cookie, err := r.Cookie("agentuse_local"); err == nil {
			credential = cookie.Value
		}
	}
	if !equal(credential) {
		http.Error(w, "请运行 agentuse ui 获取本机页面链接", 401)
		return
	}
	c.mux.ServeHTTP(w, r)
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", 404)
	} else {
		http.Error(w, "本机账本读取失败", 500)
	}
}
func filter(q url.Values) (store.Filter, error) {
	f := store.Filter{Sender: q.Get("sender"), Agent: q.Get("agent"), Status: q.Get("status")}
	var err error
	for key, dst := range map[string]*int{"limit": &f.Limit, "offset": &f.Offset} {
		if q.Get(key) != "" {
			*dst, err = strconv.Atoi(q.Get(key))
			if err != nil || *dst < 0 {
				return f, fmt.Errorf("invalid %s", key)
			}
		}
	}
	for key, dst := range map[string]*int64{"from": &f.After, "to": &f.Before} {
		if q.Get(key) != "" {
			t, e := time.Parse("2006-01-02", q.Get(key))
			if e != nil {
				return f, e
			}
			if key == "to" {
				t = t.AddDate(0, 0, 1)
			}
			*dst = t.UnixMilli()
		}
	}
	return f, nil
}
func (c *Controller) list(w http.ResponseWriter, r *http.Request) {
	f, e := filter(r.URL.Query())
	if e != nil {
		http.Error(w, "invalid filter", 400)
		return
	}
	p, e := c.reader.List(r.Context(), f)
	if e != nil {
		failure(w, e)
		return
	}
	jsonResponse(w, p)
}
func (c *Controller) detail(w http.ResponseWriter, r *http.Request) {
	v, e := c.reader.Get(r.Context(), r.PathValue("run"), r.PathValue("attempt"))
	if e != nil {
		failure(w, e)
		return
	}
	jsonResponse(w, v)
}
func (c *Controller) stats(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")
	if group == "" {
		group = "day"
	}
	if group != "day" && group != "sender" && group != "agent" && group != "harness" {
		http.Error(w, "invalid group", 400)
		return
	}
	f, e := filter(r.URL.Query())
	if e != nil {
		http.Error(w, "invalid filter", 400)
		return
	}
	v, e := c.reader.Stats(r.Context(), group, f)
	if e != nil {
		failure(w, e)
		return
	}
	jsonResponse(w, v)
}
