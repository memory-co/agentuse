package service

import (
	"bufio"
	"context"
	"encoding/json"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/memory-co/agentuse/internal/harness"
	"github.com/memory-co/agentuse/internal/protocol"
)

// localHarness 是本机一个可用的 harness。Spec 为 nil 的是自定义 harness。
type localHarness struct {
	Name    string
	Path    string
	Args    []string
	Version string
	Spec    *harness.Spec
	// Env 是这一次起进程要多带的环境变量(协调者的工作令牌等),跟着消息变。
	Env []string
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
	Usage  *agentuse.Usage `json:"usage,omitempty"`
	OK     bool            `json:"ok"`
	Output string          `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
	// SessionID 仅作为本次 harness 运行的诊断信息。
	SessionID string  `json:"session_id,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
}

// reportFn 回报一条进度(kind 是 progress、plan 或 steered),payload 是 JSON 对象。
type reportFn func(kind string, payload any)

// runHarness 每次启动新的原生会话，并通过 report 汇报进度。
// in 是这条消息收到的插话和提问答案，harness 能插话、能提问时从里面取。
func runHarness(ctx context.Context, la localHarness, m protocol.Dispatch, dir string, in *inbox, report reportFn) Result {
	if la.Spec == nil {
		in, _ := json.Marshal(protocol.HarnessInput{SessionID: m.SessionID, Instructions: instructionsFor(m), Input: m.Input, Attachments: m.Attachments, RunID: m.RunID, AgentID: m.AgentID, TaskMessageIDs: m.TaskMessageIDs, SessionMessages: m.SessionMessages})
		cmd := exec.CommandContext(ctx, la.Path, la.Args...)
		if len(la.Env) > 0 {
			cmd.Env = append(os.Environ(), la.Env...)
		}
		return runCustomHarness(ctx, cmd, in, dir, report)
	}
	var pi protocol.PromptInput
	_ = json.Unmarshal(m.Input, &pi)
	if err := la.Spec.Prepare(dir); err != nil {
		return Result{Error: "准备工作目录: " + err.Error()}
	}
	if err := writeInstructions(dir, la.Spec.Instructions, instructionsFor(m)); err != nil {
		return Result{Error: "写工作说明: " + err.Error()}
	}
	if la.Spec.Transport == harness.TransportCodexAppServer {
		return runCodex(ctx, la, m, dir, in, report)
	}
	args, err := la.Spec.Args(pi.Prompt, "", la.Args)
	if err != nil {
		return Result{Error: err.Error()}
	}
	cmd := exec.CommandContext(ctx, la.Path, args...)
	if env := append(la.Spec.Environ(m.ID, m.SessionID), la.Env...); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return runSpec(ctx, *la.Spec, cmd, la.Spec.Stdin(pi.Prompt), dir, in, report)
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
	if stdin != nil {
		cmd.Stdin = stdin
	}
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
// input 不为空时写进 stdin 并保持打开(stream-json),读到 result 行后关掉，进程才会退出。
// steer 为 claude-hooks 时先在 stdin 注册 hook,再在 hook 回调里塞插话;
// ask 为 claude-permission 时在 can_use_tool 里等用户回答提问。
func runSpec(ctx context.Context, sp harness.Spec, cmd *exec.Cmd, input []byte, dir string, in *inbox, report reportFn) Result {
	hooks := sp.Steer == harness.SteerClaudeHooks && input != nil
	control := input != nil && (hooks || sp.Ask == harness.AskClaudePermission)
	if sp.Ask != harness.AskClaudePermission && in != nil {
		in = &inbox{steers: in.steers} // 这个 harness 不能提问
	}
	var pipe io.WriteCloser
	if input != nil {
		var err error
		if pipe, err = cmd.StdinPipe(); err != nil {
			return Result{Error: err.Error()}
		}
	}
	stdout, stderr, err := start(cmd, dir, nil)
	if err != nil {
		return Result{Error: err.Error()}
	}
	closeIn := func() {}
	var w *stdinWriter
	if pipe != nil {
		w = &stdinWriter{w: pipe}
		closeIn = w.close
		go func() {
			if hooks && w.writeJSON(claudeHooksInit()) != nil {
				return
			}
			if w.write(input) != nil {
				w.close()
			}
		}()
	}
	var errTail tail
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); scanLines(stderr, errTail.add) }()

	var st harness.State
	scanLines(stdout, func(line string) {
		if control && claudeControl(line, w, in, report) {
			return
		}
		sp.Line(&st, line, func(text string) { report("progress", map[string]string{"text": text}) })
		if st.PlanDirty {
			st.PlanDirty = false
			report("plan", map[string]any{"steps": st.Plan})
		}
		if st.SawResult {
			closeIn()
		}
	})
	in.askBox().stop() // 进程的输出都读完了，还在等的提问作废
	closeIn()
	wg.Wait()
	werr := cmd.Wait()

	res := Result{Usage: st.Usage, SessionID: st.SessionID, Output: st.Output, Error: st.Error, CostUSD: st.CostUSD}
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
			report("progress", map[string]string{"text": truncate(line, 4000)})
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
		if typ == "plan" {
			report("plan", map[string]json.RawMessage{"steps": m["steps"]})
			return
		}
		report("progress", json.RawMessage(line))
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
