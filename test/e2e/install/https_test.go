package install

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestDeviceSetupBehindHTTPSProxy(t *testing.T) {
	e2eutil.Skip(t)
	if runtime.GOOS != "linux" {
		t.Skip("setup.sh installs system services on macOS; exercise its nohup branch on Linux")
	}
	gh := newFakeGitHub(t, "v9.9.9")
	dist := gh.publish(t, "v9.9.9")
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir(), "--dist", dist)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	direct := proxy.Director
	proxy.Director = func(r *http.Request) {
		direct(r)
		// Reproduce a proxy that reports its final HTTP hop, not the public TLS.
		r.Header.Set("X-Forwarded-Proto", "http")
	}
	public := httptest.NewTLSServer(proxy)
	t.Cleanup(public.Close)
	client := public.Client()

	req, err := http.NewRequest(http.MethodPost, public.URL+"/api/devices", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+srv.AdminToken)
	req.Header.Set("Referer", public.URL+"/")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var device agentuse.Device
	if err = json.NewDecoder(resp.Body).Decode(&device); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create device: status %d, error %v", resp.StatusCode, err)
	}
	if !strings.HasPrefix(device.SetupCommand, `curl -fsSL "`+public.URL+`/setup.sh"`) || !strings.Contains(device.SetupCommand, `AGENTUSE_SERVER="`+public.URL+`"`) {
		t.Fatal("installation command must retain the browser's HTTPS address")
	}
	// curl has no browser source headers. The command must carry the HTTPS
	// address into this script even though its inferred default is HTTP.
	scriptResp, err := client.Get(public.URL + "/setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	script, err := io.ReadAll(scriptResp.Body)
	scriptResp.Body.Close()
	if err != nil || !strings.Contains(string(script), `SERVER="${AGENTUSE_SERVER:-`+strings.Replace(public.URL, "https://", "http://", 1)+`}"`) {
		t.Fatal("test proxy must reproduce an HTTP default for a non-browser script request")
	}

	home := t.TempDir()
	certificate := filepath.Join(home, "proxy.pem")
	if err = os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: public.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", device.SetupCommand)
	cmd.Dir = home
	cmd.Env = e2eutil.Env(home, append(gh.env, "CURL_CA_BUNDLE="+certificate, "SSL_CERT_FILE="+certificate)...)
	t.Cleanup(func() { killPidfile(filepath.Join(home, ".agentuse", "daemon.pid")) })
	output := e2eutil.MustRun(t, cmd)
	if !strings.Contains(output, "注册成功") || !strings.Contains(output, "已在后台启动") {
		t.Fatalf("setup did not complete: %s", output)
	}
	c := agentuse.New(public.URL, srv.AdminToken)
	c.HTTPClient = client
	e2eutil.WaitFor(t, 20*time.Second, "HTTPS registered device connects through WSS", func() bool {
		devices, err := c.ListDevices(context.Background(), "")
		return err == nil && len(devices) == 1 && devices[0].ID == device.ID && devices[0].Status == "online"
	})
	requests, err := os.ReadFile(gh.log)
	if err != nil || strings.Contains(string(requests), "http://") || !strings.Contains(string(requests), public.URL+"/download/agentuse-") {
		t.Fatalf("setup downloads must remain HTTPS: %s (%v)", requests, err)
	}
}
