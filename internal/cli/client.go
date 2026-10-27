package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/store"
)

// clientConfig 是 agentuse-server login 存的 ~/.agentuse/server-cli.json。
type clientConfig struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func defaultClientConfig() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "server-cli.json")
}

func saveClientConfig(path string, c clientConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

type clientFlagSet struct{ *flag.FlagSet }

type flagsHolder struct{ server, token, config *string }

// client 按 参数 → 环境变量 → login 存的配置 的顺序找服务地址和令牌。
func (h *flagsHolder) client() (*apiClient, error) {
	var file clientConfig
	if b, err := os.ReadFile(*h.config); err == nil {
		if err := json.Unmarshal(b, &file); err != nil {
			return nil, fmt.Errorf("%s: %w", *h.config, err)
		}
	}
	pick := func(flagVal, env, fileVal string) string {
		if flagVal != "" {
			return flagVal
		}
		if v := os.Getenv(env); v != "" {
			return v
		}
		return fileVal
	}
	c := &apiClient{
		server: strings.TrimRight(pick(*h.server, "AGENTUSE_SERVER", file.Server), "/"),
		token:  pick(*h.token, "AGENTUSE_TOKEN", file.Token),
	}
	if c.server == "" || c.token == "" {
		return nil, errors.New("不知道连哪个服务：先运行 agentuse-server login -server URL -token 管理员令牌,或加 -server / -token")
	}
	return c, nil
}

// apiClient 是管理命令用的 HTTP API 客户端，接口见 docs/v2/apis/。
type apiClient struct {
	server, token string
	http          http.Client
	// unauthorized 是 401 时给的提示，空就用默认的(让管理员重新 login)。
	unauthorized string
}

type tokenResult struct {
	User     store.User `json:"user"`
	Token    string     `json:"token"`
	LoginURL string     `json:"login_url"`
}

func (c *apiClient) do(ctx context.Context, method, path string, body, out any) error {
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		switch {
		case resp.StatusCode == http.StatusUnauthorized && c.unauthorized != "":
			return errors.New(c.unauthorized)
		case resp.StatusCode == http.StatusUnauthorized:
			return errors.New("令牌无效，请重新 agentuse-server login")
		case resp.StatusCode == http.StatusForbidden && e.Error != "":
			return errors.New(e.Error)
		case resp.StatusCode == http.StatusForbidden:
			return errors.New("需要管理员令牌")
		case e.Error != "":
			return errors.New(e.Error)
		}
		return errors.New(resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *apiClient) me(ctx context.Context) (store.User, error) {
	var u store.User
	return u, c.do(ctx, http.MethodGet, "/api/me", nil, &u)
}

func (c *apiClient) listUsers(ctx context.Context) ([]store.User, error) {
	var us []store.User
	return us, c.do(ctx, http.MethodGet, "/api/users", nil, &us)
}

func (c *apiClient) createUser(ctx context.Context, id, name, role string) (tokenResult, error) {
	var res tokenResult
	return res, c.do(ctx, http.MethodPost, "/api/users", map[string]string{"id": id, "name": name, "role": role}, &res)
}

func (c *apiClient) resetToken(ctx context.Context, id string) (tokenResult, error) {
	var res tokenResult
	return res, c.do(ctx, http.MethodPost, "/api/users/"+url.PathEscape(id)+"/token", nil, &res)
}
