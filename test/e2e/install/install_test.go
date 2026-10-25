// 安装场景：照 README 的一行命令 curl … install.sh | bash 从「GitHub Release」装 agentuse-server,
// 起服务，再在另一台「用户电脑」(另一个 HOME)上跑页面给的安装命令接入设备。
// GitHub 由 testdata/curl(假 curl)在本地扮演：Release 里放的是这次编出来的二进制。
package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

func TestMain(m *testing.M) { e2eutil.Main(m) }

var platforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}

// fakeGitHub 准备假 curl 需要的东西，返回放假命令的目录和环境变量。
type fakeGitHub struct {
	bin      string // 假 curl / systemctl 所在目录，放在 PATH 最前面
	releases string // <tag>/<文件>
	log      string
	env      []string
}

func newFakeGitHub(t *testing.T, latest string) *fakeGitHub {
	t.Helper()
	dir := t.TempDir()
	g := &fakeGitHub{bin: filepath.Join(dir, "bin"), releases: filepath.Join(dir, "releases"), log: filepath.Join(dir, "curl.log")}
	for _, name := range []string{"curl", "systemctl"} {
		copyFile(t, filepath.Join("testdata", name), filepath.Join(g.bin, name), 0o755)
	}
	realCurl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("e2e 安装场景需要 curl")
	}
	installSh, _ := filepath.Abs(filepath.Join("..", "..", "..", "install.sh"))
	g.env = []string{
		"PATH=" + g.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"REAL_CURL=" + realCurl,
		"FAKE_INSTALL_SH=" + installSh,
		"FAKE_LATEST=" + latest,
		"FAKE_RELEASES=" + g.releases,
		"FAKE_CURL_LOG=" + g.log,
	}
	return g
}

// publish 像 release.yml 那样发一个版本:各平台 agentuse、本机平台的 agentuse-server、SHA256SUMS。
// 只编本机平台，其它平台的 agentuse 用同一个文件代替(install.sh 只下载、校验、摆放，不执行它们)。
func (g *fakeGitHub) publish(t *testing.T, tag string) string {
	t.Helper()
	dir := filepath.Join(g.releases, tag)
	host := runtime.GOOS + "-" + runtime.GOARCH
	files := map[string]string{"agentuse-server-" + host: e2eutil.Binary(t, "agentuse-server")}
	for _, p := range platforms {
		files["agentuse-"+p] = e2eutil.Binary(t, "agentuse")
	}
	var sums strings.Builder
	for name, src := range files {
		copyFile(t, src, filepath.Join(dir, name), 0o755)
		fmt.Fprintf(&sums, "%s  %s\n", sha256File(t, src), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// oneLiner 是 README 里的安装命令，测试跟着 README 走。
func oneLiner(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := regexp.MustCompile(`curl -fsSL https://raw\.githubusercontent\.com/memory-co/agentuse/main/install\.sh \| bash`).Find(b)
	if cmd == nil {
		t.Fatal("README 里找不到 install.sh 的一行安装命令")
	}
	return string(cmd)
}

func TestInstall(t *testing.T) {
	e2eutil.Skip(t)
	gh := newFakeGitHub(t, "v9.9.9")
	gh.publish(t, "v9.9.9")
	line := oneLiner(t)

	// 服务器：一行命令装最新版
	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	cmd := exec.Command("bash", "-c", line)
	cmd.Env = e2eutil.Env(home, append(gh.env, "INSTALL_DIR="+installDir)...)
	out := e2eutil.MustRun(t, cmd)
	for _, want := range []string{"安装 agentuse v9.9.9", "装好了", "不在 PATH 里", "agentuse-server start\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("install.sh 输出缺 %q:\n%s", want, out)
		}
	}
	server := filepath.Join(installDir, "agentuse-server")
	if v := strings.TrimSpace(e2eutil.MustRun(t, exec.Command(server, "version"))); v != "e2e" {
		t.Fatalf("装上的 agentuse-server version: %q", v)
	}
	dist := filepath.Join(home, ".agentuse", "dist")
	for _, p := range platforms {
		if st, err := os.Stat(filepath.Join(dist, "agentuse-"+p)); err != nil || st.Mode()&0o111 == 0 {
			t.Fatalf("dist 里的 agentuse-%s: %v", p, err)
		}
	}
	if log, _ := os.ReadFile(gh.log); !strings.Contains(string(log), "/releases/latest") {
		t.Fatalf("没有查最新版本:\n%s", log)
	}

	// 起服务:install.sh 放好的 dist 就是默认目录，不该有缺二进制的警告
	srv := e2eutil.StartServer(t, server, home)
	if strings.Contains(srv.Proc.Output(), "警告") {
		t.Fatalf("服务启动有警告:\n%s", srv.Proc.Output())
	}

	t.Run("setup.sh", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("setup.sh 在 macOS 上会装 launchd 服务，只在 linux 上走 nohup 分支测")
		}
		testSetup(t, gh, srv)
	})
}

// testSetup:用户电脑(另一个 HOME)上跑页面给的安装命令，daemon 装好并连上服务。
func testSetup(t *testing.T, gh *fakeGitHub, srv *e2eutil.Server) {
	ctx := context.Background()
	c := agentuse.New(srv.URL, srv.AdminToken)
	dv, err := c.CreateDaemon(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	device := t.TempDir()
	cmd := exec.Command("bash", "-c", dv.SetupCommand)
	cmd.Dir = device
	cmd.Env = e2eutil.Env(device, gh.env...)
	t.Cleanup(func() { killPidfile(filepath.Join(device, ".agentuse", "daemon.pid")) })
	out := e2eutil.MustRun(t, cmd)
	if !strings.Contains(out, "注册成功") || !strings.Contains(out, "已在后台启动") {
		t.Fatalf("setup.sh:\n%s", out)
	}

	e2eutil.WaitFor(t, 20*time.Second, "设备上线", func() bool {
		ds, err := c.ListDaemons(ctx, "alice")
		return err == nil && len(ds) == 1 && ds[0].ID == dv.ID && ds[0].Status == "online"
	})
	ds, _ := c.ListDaemons(ctx, "alice")
	if host, _ := os.Hostname(); ds[0].Name != host {
		t.Fatalf("没起名字的设备用主机名: %q", ds[0].Name)
	}

	bin := filepath.Join(device, ".agentuse", "bin", "agentuse")
	status := exec.Command(bin, "status")
	status.Env = e2eutil.Env(device)
	if out := e2eutil.MustRun(t, status); !strings.Contains(out, "正常") || !strings.Contains(out, "nohup: 运行中") {
		t.Fatalf("agentuse status:\n%s", out)
	}
}

// 校验和对不上时什么都不装。
func TestInstallChecksumMismatch(t *testing.T) {
	e2eutil.Skip(t)
	gh := newFakeGitHub(t, "v9.9.9")
	dir := gh.publish(t, "v0.0.1")
	f, err := os.OpenFile(filepath.Join(dir, "agentuse-linux-arm64"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("tampered")
	f.Close()

	home := t.TempDir()
	installDir := filepath.Join(home, "bin")
	cmd := exec.Command("bash", "-c", oneLiner(t))
	cmd.Env = e2eutil.Env(home, append(gh.env, "INSTALL_DIR="+installDir, "VERSION=v0.0.1")...)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "校验失败") {
		t.Fatalf("应该校验失败: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(installDir, "agentuse-server")); !os.IsNotExist(err) {
		t.Fatalf("校验失败时不该装上: %v", err)
	}
	if log, _ := os.ReadFile(gh.log); strings.Contains(string(log), "/releases/latest") {
		t.Fatal("指定 VERSION 时不该查最新版本")
	}
}

// 版本不存在时报错退出。
func TestInstallMissingRelease(t *testing.T) {
	e2eutil.Skip(t)
	gh := newFakeGitHub(t, "v9.9.9")
	home := t.TempDir()
	cmd := exec.Command("bash", "-c", oneLiner(t))
	cmd.Env = e2eutil.Env(home, append(gh.env, "INSTALL_DIR="+filepath.Join(home, "bin"))...)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "下载 https://github.com/memory-co/agentuse/releases/download/v9.9.9/SHA256SUMS 失败") {
		t.Fatalf("应该下载失败: %v\n%s", err, out)
	}
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func killPidfile(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
		syscall.Kill(pid, syscall.SIGTERM)
	}
}
