package install

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

// A controlling terminal is necessary: stdin contains the installer when using
// curl | bash. Python's stdlib pty keeps this test portable across Linux/macOS.
const terminalInstaller = `import errno,json,os,pty,select,signal,sys,time
answers=json.load(sys.stdin)
pid,fd=pty.fork()
if pid==0: os.execl('/bin/bash','bash','-c',sys.argv[1])
output=b'';sent=0;deadline=time.monotonic()+45
try:
 while time.monotonic()<deadline:
  if select.select([fd],[],[],.2)[0]:
   try: chunk=os.read(fd,65536)
   except OSError as error:
    if error.errno==errno.EIO: break
    raise
   if not chunk: break
   output+=chunk
   prompts=output.count('输入后将后台启动或重启 Server'.encode())+output.count('再次输入密码:'.encode())
   while sent<prompts:
    if sent>=len(answers): raise RuntimeError('unexpected installer prompt')
    os.write(fd,(answers[sent]+'\n').encode());sent+=1
 else:
  os.kill(pid,signal.SIGTERM)
  raise RuntimeError('installer timed out')
 _,status=os.waitpid(pid,0)
 sys.stdout.buffer.write(output)
 sys.exit(os.waitstatus_to_exitcode(status))
finally: os.close(fd)
`

func installInTerminal(t *testing.T, gh *fakeGitHub, home string, answers []string) string {
	t.Helper()
	cmd := terminalInstallerCommand(t, gh, home, answers)
	out := e2eutil.MustRun(t, cmd)
	for _, password := range answers {
		if password != "" && strings.Contains(out, password) {
			t.Fatal("installer echoed a chosen password")
		}
	}
	return out
}

func terminalInstallerCommand(t *testing.T, gh *fakeGitHub, home string, answers []string) *exec.Cmd {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("interactive installer test requires python3")
	}
	input, _ := json.Marshal(answers)
	cmd := exec.Command(python, "-c", terminalInstaller, oneLiner(t))
	cmd.Stdin = strings.NewReader(string(input))
	cmd.Env = e2eutil.Env(home, append(gh.env, "INSTALL_DIR="+filepath.Join(home, "bin"), "AGENTUSE_ADDR=127.0.0.1:0")...)
	return cmd
}

func TestInstallPasswordRejectsUnsupportedReleaseBeforeStarting(t *testing.T) {
	e2eutil.Skip(t)
	gh := newFakeGitHub(t, "v9.9.9")
	release := gh.publish(t, "v9.9.9")
	name := "agentuse-server-" + runtime.GOOS + "-" + runtime.GOARCH
	path := filepath.Join(release, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'legacy start options'\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(release, "SHA256SUMS")
	raw, err := os.ReadFile(sums)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if strings.HasSuffix(line, "  "+name) {
			lines[i] = sha256File(t, path) + "  " + name
		}
	}
	if err = os.WriteFile(sums, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := terminalInstallerCommand(t, gh, home, []string{"custom-password", "custom-password"})
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "尚不支持安装时设置密码") || strings.Contains(string(out), "custom-password") {
		t.Fatal("unsupported binary was not rejected before startup")
	}
	if _, err = os.Stat(filepath.Join(home, ".agentuse", "server.db")); !os.IsNotExist(err) {
		t.Fatal("unsupported binary changed Server data", err)
	}
}

func TestInstallChosenPasswordRestartsServerAndEmptyInputPreservesIt(t *testing.T) {
	e2eutil.Skip(t)
	gh := newFakeGitHub(t, "v9.9.9")
	gh.publish(t, "v9.9.9")
	home := t.TempDir()
	pidfile := filepath.Join(home, ".agentuse", "server.pid")
	t.Cleanup(func() { killPidfile(pidfile) })
	first := "安装密码 +%\";$()&!"
	out := installInTerminal(t, gh, home, []string{first, first})
	if !strings.Contains(out, "管理员登录密码已设置") {
		t.Fatal(out)
	}
	firstPID, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	url, logs := installedServer(t, home)
	admin := assertPasswordLogin(t, url, first, true)
	if strings.Contains(logs, first) || strings.Contains(logs, "login?token=") {
		t.Fatal("custom credential leaked into Server startup logs")
	}
	second := "updated password #2"
	installInTerminal(t, gh, home, []string{second, second})
	secondPID, err := os.ReadFile(pidfile)
	if err != nil || string(secondPID) == string(firstPID) {
		t.Fatal("installer did not replace the Server process", err)
	}
	url, logs = installedServer(t, home)
	assertPasswordLogin(t, url, first, false)
	if updated := assertPasswordLogin(t, url, second, true); updated.ID != admin.ID {
		t.Fatal("changing password created a second administrator")
	}
	if strings.Contains(logs, second) {
		t.Fatal("new password leaked into logs")
	}
	if output := installInTerminal(t, gh, home, []string{""}); !strings.Contains(output, "agentuse-server start") {
		t.Fatal(output)
	}
	unchangedPID, err := os.ReadFile(pidfile)
	if err != nil || string(unchangedPID) != string(secondPID) {
		t.Fatal("empty input restarted the Server", err)
	}
	assertPasswordLogin(t, url, second, true)
}

func TestInstallPasswordMismatchCanBeSkipped(t *testing.T) {
	e2eutil.Skip(t)
	gh := newFakeGitHub(t, "v9.9.9")
	gh.publish(t, "v9.9.9")
	home := t.TempDir()
	out := installInTerminal(t, gh, home, []string{"first-password", "different-password", ""})
	if !strings.Contains(out, "两次输入不一致") || !strings.Contains(out, "agentuse-server start") {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(home, ".agentuse", "server.db")); !os.IsNotExist(err) {
		t.Fatal("skipping password unexpectedly initialized the Server", err)
	}
}

func installedServer(t *testing.T, home string) (string, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".agentuse", "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`agentuse 服务已启动: (http://\S+)`).FindStringSubmatch(string(raw))
	if len(match) != 2 {
		t.Fatal("installer did not start the Server")
	}
	return match[1], string(raw)
}

func assertPasswordLogin(t *testing.T, base, password string, valid bool) agentuse.User {
	t.Helper()
	var user agentuse.User
	e2eutil.WaitFor(t, 3*time.Second, "installed Server HTTP listener", func() bool {
		body, _ := json.Marshal(map[string]string{"token": password})
		response, err := http.Post(base+"/api/login", "application/json", strings.NewReader(string(body)))
		if err != nil {
			return false
		}
		defer response.Body.Close()
		if !valid {
			return response.StatusCode == http.StatusUnauthorized
		}
		if response.StatusCode != http.StatusOK {
			return false
		}
		if err = json.NewDecoder(response.Body).Decode(&user); err != nil || !user.IsAdmin() {
			return false
		}
		req, _ := http.NewRequestWithContext(context.Background(), "GET", base+"/api/me", nil)
		for _, cookie := range response.Cookies() {
			req.AddCookie(cookie)
		}
		me, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer me.Body.Close()
		return me.StatusCode == http.StatusOK
	})
	return user
}
