// Package e2eutil 是 test/e2e 各场景共用的工具：编译被测二进制、起 agentuse-server、跑子进程、等条件成立。
//
// e2e 测的是编好的二进制(和发布出去的一样),不是测试进程里的对象。每个场景是 test/e2e 下的一个包，
// 在 TestMain 里调用 Main;go test -short 时整个跳过。
package e2eutil

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const module = "github.com/memory-co/agentuse"

// 被测的包，key 是编出来的文件名。
var packages = map[string]string{
	"agentuse":        module + "/client/cmd/agentuse",
	"agentuse-server": module + "/server/cmd/agentuse-server",
	"fakeharness":     module + "/test/e2e/fakeharness",
}

var (
	binDir string
	builds sync.Map // name → *build
)

type build struct {
	once sync.Once
	path string
	err  error
}

// Main 是场景包的 TestMain:准备放二进制的临时目录，跑完删掉。
func Main(m *testing.M) {
	flag.Parse()
	dir, err := os.MkdirTemp("", "agentuse-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// Skip 在 -short 或不支持的系统上跳过。每个 e2e 测试第一行调用。
func Skip(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e:-short 时跳过")
	}
	if runtime.GOOS == "windows" {
		t.Skip("e2e:只在 linux / macOS 上跑")
	}
}

// Binary 编译(每个进程只编一次)并返回二进制路径。name 是 agentuse、agentuse-server 或 fakeharness。
func Binary(t *testing.T, name string) string {
	t.Helper()
	pkg, ok := packages[name]
	if !ok {
		t.Fatalf("未知二进制 %s", name)
	}
	v, _ := builds.LoadOrStore(name, &build{})
	b := v.(*build)
	b.once.Do(func() {
		b.path = filepath.Join(binDir, name)
		cmd := exec.Command("go", "build", "-ldflags", "-X "+module+"/internal/version.Version=e2e", "-o", b.path, pkg)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.err = fmt.Errorf("go build %s: %v\n%s", pkg, err, out)
		}
	})
	if b.err != nil {
		t.Fatal(b.err)
	}
	return b.path
}

// Proc 是一个后台子进程，输出收进 Output(),测试结束时杀掉。
type Proc struct {
	Cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
	out  bytes.Buffer
}

func (p *Proc) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.Write(b)
}

func (p *Proc) Output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// Start 在后台起 cmd,stdout / stderr 合在一起收集。测试失败时打印出来。
func Start(t *testing.T, cmd *exec.Cmd) *Proc {
	t.Helper()
	p := &Proc{Cmd: cmd, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p, p
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", cmd.Path, err)
	}
	go func() { cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		p.Stop()
		if t.Failed() {
			t.Logf("%s 的输出:\n%s", filepath.Base(cmd.Path), p.Output())
		}
	})
	return p
}

// Stop 先 SIGINT,5 秒没退出再 SIGKILL,等到进程结束。可以重复调用。
func (p *Proc) Stop() {
	p.Cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		p.Cmd.Process.Kill()
		<-p.done
	}
}

// Server 是一个跑起来的 agentuse-server。
type Server struct {
	URL        string
	AdminToken string
	Proc       *Proc
}

var (
	startedRe = regexp.MustCompile(`agentuse 服务已启动: (http://\S+)`)
	tokenRe   = regexp.MustCompile(`管理员登录令牌\(只显示这一次\): (ut_[0-9a-f]+)`)
)

// StartServer 用 bin(agentuse-server)在随机端口起服务，数据放在 home 下，等到打印出地址和管理员令牌。
// extra 追加到 start 后面，比如 -dist。
func StartServer(t *testing.T, bin, home string, extra ...string) *Server {
	t.Helper()
	args := append([]string{"start", "-addr", "127.0.0.1:0", "-db", filepath.Join(home, "server.db")}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Env = Env(home)
	p := Start(t, cmd)
	s := &Server{Proc: p}
	WaitFor(t, 20*time.Second, "服务启动", func() bool {
		out := p.Output()
		u, tok := startedRe.FindStringSubmatch(out), tokenRe.FindStringSubmatch(out)
		if u == nil || tok == nil {
			return false
		}
		s.URL, s.AdminToken = u[1], tok[1]
		return true
	})
	return s
}

// Env 是子进程的环境:HOME 换成 home(~/.agentuse 都落在里面),去掉会影响命令的 AGENTUSE_* 变量。
func Env(home string, kv ...string) []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "HOME=") || strings.HasPrefix(e, "AGENTUSE_") || strings.HasPrefix(e, "TOKEN=") {
			continue
		}
		env = append(env, e)
	}
	return append(append(env, "HOME="+home), kv...)
}

// MustRun 跑一个命令到结束，返回合并的输出;失败就 Fatal。
func MustRun(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	b, err := cmd.CombinedOutput()
	out := string(b)
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(cmd.Args, " "), err, out)
	}
	return out
}

// WaitFor 每 100ms 检查一次 cond,timeout 内不成立就 Fatal。
func WaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}
