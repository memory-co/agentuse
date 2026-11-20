package controller

import (
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
	if !strings.Contains(script, `SERVER="`+e.ts.URL+`"`) || strings.Contains(script, "__SERVER__") ||
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
	want := "agentuse register --server '" + publicURL + "' --server-pin '" + device.SetupServerPin + "'"
	if resp.StatusCode != http.StatusCreated || device.SetupCommand != want {
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
	if err != nil || !strings.Contains(string(script), `SERVER="`+publicURL+`"`) {
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

func TestDevicePrivateKeyIssuanceRequiresTrustedManagementConnection(t *testing.T) {
	e := newEnv(t, Config{PublicURL: "https://agentuse.example.com"})
	_, token := e.user("key-issuer", agentuse.RoleUser)
	for _, tc := range []struct {
		name           string
		remote         string
		target         string
		forwardedProto string
		want           int
	}{
		{"remote HTTP", "192.0.2.10:8040", "http://agentuse.example.com/api/devices", "", 403},
		{"remote forged forwarding header", "192.0.2.10:8040", "http://agentuse.example.com/api/devices", "https", 403},
		{"remote forged localhost", "192.0.2.10:8040", "http://localhost/api/devices", "", 403},
		{"loopback", "127.0.0.1:8040", "http://localhost/api/devices", "", 201},
		{"local HTTPS proxy", "127.0.0.1:8040", "http://agentuse.example.com/api/devices", "https", 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.target, nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			w := httptest.NewRecorder()
			e.srv.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
			if tc.want == 403 && strings.Contains(w.Body.String(), "setup_token") {
				t.Fatal("response leaked a device key")
			}
		})
	}
}
