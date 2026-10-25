package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/memory-co/agentuse/harness"
	"github.com/memory-co/agentuse/internal/protocol"
)

// localHarness 是本机一个可用的 harness。Spec 为 nil 的是自定义 harness。
type localHarness struct {
	Name    string
	Path    string
	Args    []string
	Version string
	Spec    *harness.Spec
}

// detectHarnesses 找出本机可用的 harness:内置的(harness/*/harness.yaml)按默认命令探测，配置文件里的按配置。
func detectHarnesses(cfg Config) map[string]localHarness {
	out := map[string]localHarness{}
	want := map[string]HarnessConfig{}
	builtins, _ := harness.All()
	for _, sp := range builtins {
		want[sp.Name] = HarnessConfig{Command: sp.Command}
	}
	for name, ac := range cfg.Harnesses {
		if sp, ok := harness.Get(name); ok && ac.Command == "" {
			ac.Command = sp.Command
		}
		want[name] = ac
	}
	for name, ac := range want {
		if ac.Disabled || ac.Command == "" {
			continue
		}
		path, err := exec.LookPath(expandHome(ac.Command))
		if err != nil {
			continue
		}
		la := localHarness{Name: name, Path: path, Args: ac.Args}
		if sp, ok := harness.Get(name); ok {
			la.Spec = &sp
			if len(sp.Detect.Version) > 0 {
				la.Version = probeVersion(path, sp.Detect.Version)
			}
		}
		out[name] = la
	}
	return out
}

func probeVersion(path string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return truncate(line, 60)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return home + p[1:]
	}
	return p
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Result 是 harness 回一条消息的最终结果，作为 done / failed 的 payload。
type Result struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	// SessionID 是 harness 自己的会话 id,服务存下来，下次通过 Dispatch.Resume 带回来续接。
	SessionID string  `json:"session_id,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
}

// reportFn 回报一条进度,payload 是 JSON 对象。
type reportFn func(payload any)

// runHarness 在 dir 里跑 harness 回一条消息，过程中调用 report 回报进度。m.Resume 不为空时续接 harness 的会话。
func runHarness(ctx context.Context, la localHarness, m protocol.Dispatch, dir string, report reportFn) Result {
	if la.Spec == nil {
		in, _ := json.Marshal(protocol.HarnessInput{SessionID: m.SessionID, Resume: m.Resume, Input: m.Input})
		return withSession(runCustomHarness(ctx, exec.CommandContext(ctx, la.Path, la.Args...), in, dir, report), m.Resume)
	}
	var pi protocol.PromptInput
	_ = json.Unmarshal(m.Input, &pi)
	args, err := la.Spec.Args(pi.Prompt, m.Resume, la.Args)
	if err != nil {
		return Result{Error: err.Error()}
	}
	return withSession(runSpec(ctx, *la.Spec, exec.CommandContext(ctx, la.Path, args...), dir, report), m.Resume)
}

// withSession:harness 没报会话 id 时沿用续接的那个，免得服务那边丢掉。
func withSession(r Result, resume string) Result {
	if r.SessionID == "" {
		r.SessionID = resume
	}
	return r
}

const maxOutput = 64 << 10

// tail 只保留最后 maxOutput 字节的输出。
type tail struct{ b []byte }

func (t *tail) add(s string) {
	t.b = append(t.b, s...)
	t.b = append(t.b, '\n')
	if len(t.b) > maxOutput {
		t.b = t.b[len(t.b)-maxOutput:]
	}
}
func (t *tail) String() string { return strings.TrimRight(string(t.b), "\n") }

func scanLines(r io.Reader, fn func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		fn(sc.Text())
	}
}

func start(cmd *exec.Cmd, dir string, stdin io.Reader) (stdout, stderr io.ReadCloser, err error) {
	cmd.Dir = dir
	cmd.Stdin = stdin
	if stdout, err = cmd.StdoutPipe(); err != nil {
		return
	}
	if stderr, err = cmd.StderrPipe(); err != nil {
		return
	}
	err = cmd.Start()
	return
}

// runSpec 按 harness.yaml 的 output 规则读 stdout,按 finish 判成败。
func runSpec(ctx context.Context, sp harness.Spec, cmd *exec.Cmd, dir string, report reportFn) Result {
	stdout, stderr, err := start(cmd, dir, nil)
	if err != nil {
		return Result{Error: err.Error()}
	}
	var errTail tail
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); scanLines(stderr, errTail.add) }()

	var st harness.State
	scanLines(stdout, func(line string) {
		sp.Line(&st, line, func(text string) { report(map[string]string{"text": text}) })
	})
	wg.Wait()
	werr := cmd.Wait()

	res := Result{SessionID: st.SessionID, Output: st.Output, Error: st.Error, CostUSD: st.CostUSD}
	switch {
	case sp.Finish.RequireResult:
		res.OK = st.SawResult && st.ResultOK && werr == nil
		if !st.SawResult || (st.ResultOK && werr != nil) {
			res.Error = exitError(ctx, werr, errTail.String())
		}
	case sp.Finish.RequireOK:
		res.OK = st.SawOK && werr == nil
		if res.OK {
			res.Error = ""
		} else if res.Error == "" || werr != nil {
			msg := exitError(ctx, werr, errTail.String())
			if res.Error != "" {
				msg = res.Error + "; " + msg
			}
			res.Error = msg
		}
	default:
		res.OK = werr == nil
		if !res.OK {
			res.Error = exitError(ctx, werr, errTail.String())
		}
	}
	if res.OK {
		res.Error = ""
	}
	return res
}

// runCustomHarness 按自定义 harness 的约定跑:HarnessInput 写进 stdin,stdout 每行一个 JSON。
//
//	{"type":"progress", ...任意字段}         → 原样作为进度回报
//	{"type":"result","ok":true,"output":"…","session_id":"…"} → 最终结果
//
// 不是 JSON 的行当作文本进度。没输出 result 就按退出码判断成败。
func runCustomHarness(ctx context.Context, cmd *exec.Cmd, input json.RawMessage, dir string, report reportFn) Result {
	stdout, stderr, err := start(cmd, dir, strings.NewReader(string(input)+"\n"))
	if err != nil {
		return Result{Error: err.Error()}
	}
	var out, errTail tail
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); scanLines(stderr, errTail.add) }()

	var res *Result
	scanLines(stdout, func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &m) != nil {
			out.add(line)
			report(map[string]string{"text": truncate(line, 4000)})
			return
		}
		var typ string
		_ = json.Unmarshal(m["type"], &typ)
		if typ == "result" {
			var r Result
			_ = json.Unmarshal([]byte(line), &r)
			res = &r
			return
		}
		report(json.RawMessage(line))
	})
	wg.Wait()
	werr := cmd.Wait()
	if res != nil {
		if werr != nil && res.OK {
			res.OK, res.Error = false, exitError(ctx, werr, errTail.String())
		}
		return *res
	}
	if werr != nil {
		return Result{Output: out.String(), Error: exitError(ctx, werr, errTail.String())}
	}
	return Result{OK: true, Output: out.String()}
}

func exitError(ctx context.Context, err error, stderr string) string {
	if ctx.Err() != nil {
		return "已取消"
	}
	msg := "进程没有给出结果"
	if err != nil {
		msg = err.Error()
	}
	if stderr != "" {
		msg += ": " + truncate(stderr, 2000)
	}
	return msg
}
