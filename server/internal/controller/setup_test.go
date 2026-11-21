package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestSetupScript(t *testing.T) {
	e := newEnv(t, Config{})
	resp, err := http.Get(e.ts.URL + "/setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	script := string(b)
	if !strings.Contains(script, "SERVER='"+e.ts.URL+"'") || strings.Contains(script, "__SERVER__") ||
		resp.Header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(script, "#!/usr/bin/env bash") {
		t.Fatalf("setup.sh: %v\n%s", resp.Header, script)
	}
}

func TestConfiguredPublicURL(t *testing.T) {
	const publicURL = "https://agentuse.example.com:8443"
	e := newEnv(t, Config{PublicURL: publicURL + "/"})
	_, token := e.user("admin", agentuse.RoleAdmin)
	headers := []string{"X-Forwarded-Host", "proxy.invalid", "X-Forwarded-Proto", "http", "Origin", "https://browser.invalid", "Referer", "https://browser.invalid/"}
	var device deviceView
	resp := e.req(token, "POST", "/api/devices", nil, &device, headers...)
	if resp.StatusCode != http.StatusCreated ||
		!strings.Contains(device.SetupCommand, "curl -fsSL '"+publicURL+"/setup.sh'") ||
		!strings.Contains(device.SetupCommand, "register --server '"+publicURL+"' --server-pin '"+device.SetupServerPin+"'") {
		t.Fatal("device installation command must use the configured public URL")
	}
	var user agentuse.UserToken
	resp = e.req(token, "POST", "/api/users", map[string]string{"name": "alice"}, &user, headers...)
	if resp.StatusCode != http.StatusCreated || user.LoginURL != publicURL+"/login?token="+user.Token {
		t.Fatal("login links must use the configured public URL")
	}
	req, err := http.NewRequest("GET", e.ts.URL+"/setup.sh", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	script, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(script), "SERVER='"+publicURL+"'") {
		t.Fatal("setup.sh must use the same configured public URL")
	}
}

func TestDownload(t *testing.T) {
	dist := t.TempDir()
	os.WriteFile(filepath.Join(dist, "agentuse-linux-amd64"), []byte("BIN"), 0o755)
	os.WriteFile(filepath.Join(dist, "secret.txt"), []byte("no"), 0o644)
	e := newEnv(t, Config{DistDir: dist})

	get := func(name string) (int, string) {
		resp, err := http.Get(e.ts.URL + "/download/" + name)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("agentuse-linux-amd64"); code != 200 || body != "BIN" {
		t.Fatalf("download: %d %q", code, body)
	}
	if code, body := get("agentuse-darwin-arm64"); code != 404 || !strings.Contains(body, "darwin/arm64") {
		t.Fatalf("missing platform: %d %q", code, body)
	}
	// 只给 agentuse-<os>-<arch>,别的文件拿不到
	for _, name := range []string{"secret.txt", "agentuse-windows-amd64", "..%2Fsecret.txt"} {
		if code, _ := get(name); code != 404 {
			t.Fatalf("%s: %d", name, code)
		}
	}
	// 没配 dist 目录
	e2 := newEnv(t, Config{})
	if resp, _ := http.Get(e2.ts.URL + "/download/agentuse-linux-amd64"); resp.StatusCode != 404 {
		t.Fatalf("no dist: %d", resp.StatusCode)
	}
}

func TestMissingDist(t *testing.T) {
	if got := MissingDist(""); !reflect.DeepEqual(got, Platforms) {
		t.Fatalf("empty dir: %v", got)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "agentuse-linux-amd64"), nil, 0o755)
	os.Mkdir(filepath.Join(dir, "agentuse-darwin-arm64"), 0o755) // 目录不算
	if got := MissingDist(dir); !reflect.DeepEqual(got, []string{"linux-arm64", "darwin-amd64", "darwin-arm64"}) {
		t.Fatalf("missing: %v", got)
	}
}

func TestDevicePrivateKeyIssuanceOverHTTP(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			e := newEnv(t, Config{PublicURL: scheme + "://agentuse.example.com"})
			_, token := e.user("key-issuer", agentuse.RoleUser)
			post := func(path, token string, want int) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(http.MethodPost, "http://agentuse:8080"+path, nil)
				r.RemoteAddr = "192.0.2.10:8040"
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				e.srv.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("POST %s returned %d, want %d", path, w.Code, want)
				}
				return w
			}
			decodeKey := func(w *httptest.ResponseRecorder) deviceView {
				t.Helper()
				var device deviceView
				if err := json.NewDecoder(w.Body).Decode(&device); err != nil {
					t.Fatal(err)
				}
				if device.ID == "" || device.SetupToken == "" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("missing device ID, private key, or no-store response header")
				}
				return device
			}
			post("/api/devices", "", http.StatusUnauthorized)
			created := decodeKey(post("/api/devices", token, http.StatusCreated))
			resetPath := "/api/devices/" + created.ID + "/reset"
			post(resetPath, "", http.StatusUnauthorized)
			reset := decodeKey(post(resetPath, token, http.StatusOK))
			if reset.ID != created.ID || reset.SetupToken == created.SetupToken {
				t.Fatal("reset did not issue a new key for the same device")
			}
		})
	}
}
