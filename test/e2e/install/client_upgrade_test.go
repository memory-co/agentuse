package install

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestClientUpgradeFromDevices(t *testing.T) {
	e2eutil.Skip(t)
	if (runtime.GOOS != "linux" && runtime.GOOS != "darwin") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("supported binary platforms")
	}
	const oldVersion, newVersion = "v9.0.0", "v9.1.0"
	const mod = "github.com/memory-co/agentuse/"
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	build := func(pkg, tag, path, otherFlags string) {
		t.Helper()
		flags := "-X " + mod + "internal/version.Version=" + tag + otherFlags
		cmd := exec.Command("go", "build", "-ldflags", flags, "-o", path, pkg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		e2eutil.MustRun(t, cmd)
	}
	serverHome, deviceHome := t.TempDir(), t.TempDir()
	next := filepath.Join(serverHome, "next-client")
	build("./client/cmd/agentuse", newVersion, next, "")
	binary, err := os.ReadFile(next)
	if err != nil {
		t.Fatal(err)
	}
	asset := "agentuse-" + runtime.GOOS + "-" + runtime.GOARCH
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": newVersion})
		case "/download/" + newVersion + "/SHA256SUMS":
			_, _ = fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(binary), asset)
		case "/download/" + newVersion + "/" + asset:
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer releases.Close()
	serverBin, installed := filepath.Join(serverHome, "agentuse-server"), filepath.Join(deviceHome, "agentuse")
	build("./server/cmd/agentuse-server", oldVersion, serverBin, " -X "+mod+"server/internal/service/upgrade.githubAPIURL="+releases.URL+"/latest")
	build("./client/cmd/agentuse", oldVersion, installed, " -X "+mod+"client/internal/cli.clientReleaseURL="+releases.URL+"/download")
	srv := e2eutil.StartServer(t, serverBin, serverHome)
	api := agentuse.New(srv.URL, srv.AdminToken)
	ctx := context.Background()
	device, err := api.CreateDevice(ctx, "alice", "upgrade-client")
	if err != nil {
		t.Fatal(err)
	}
	register := exec.Command(installed, "register", "--server", srv.URL, "--server-pin", device.SetupServerPin)
	register.Stdin, register.Env = strings.NewReader(device.SetupToken), e2eutil.Env(deviceHome)
	e2eutil.MustRun(t, register)
	daemon := exec.Command(installed, "daemon")
	daemon.Env = e2eutil.Env(deviceHome)
	p := e2eutil.Start(t, daemon)
	devicesMatch := func(version, status string) bool {
		ds, err := api.ListDevices(ctx, "alice")
		if err != nil || len(ds) != 1 || ds[0].ID != device.ID || ds[0].Status != agentuse.DeviceOnline || ds[0].ClientVersion != version {
			return false
		}
		return status == "" || (ds[0].ClientUpgrade != nil && ds[0].ClientUpgrade.Status == status)
	}
	e2eutil.WaitFor(t, 20*time.Second, "Client 上报旧版本", func() bool { return devicesMatch(oldVersion, "") })
	if release, err := api.LatestClientRelease(ctx, "alice"); err != nil || release.Version != newVersion {
		t.Fatalf("latest release: %+v, %v", release, err)
	}
	if _, err := api.UpgradeDevice(ctx, "bob", device.ID); err == nil {
		t.Fatal("unrelated user can upgrade the device")
	}
	if accepted, err := api.UpgradeDevice(ctx, "alice", device.ID); err != nil || accepted.TargetVersion != newVersion {
		t.Fatalf("upgrade request: %+v, %v", accepted, err)
	}
	e2eutil.WaitFor(t, 30*time.Second, "Client 从 WebSocket 升级并上线", func() bool { return devicesMatch(newVersion, "succeeded") })
	if err := p.Cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("original Client PID was not preserved: %v", err)
	}
	if got := strings.TrimSpace(e2eutil.MustRun(t, exec.Command(installed, "version"))); got != newVersion {
		t.Fatalf("installed Client: got %q, want %s", got, newVersion)
	}
	if _, err := api.UpgradeDevice(ctx, "alice", device.ID); err == nil {
		t.Fatal("no-op upgrade was accepted")
	}
}
