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

	"github.com/memory-co/agentuse/internal/protocol"
)

// builtins 是自带适配的 agent:名字 → 默认命令。
var builtins = map[string]string{
	"claude": "claude",
	"codex":  "codex",
}

// localAgent 是本机一个可用的 agent。
type localAgent struct {
	Name    string
	Path    string
	Args    []string
	Version string
}

// detectAgents 找出本机可用的 agent:内置的按默认命令探测，配置文件里的按配置。
func detectAgents(cfg Config) map[string]localAgent {
	out := map[string]localAgent{}
	want := map[string]AgentConfig{}
	for name, cmd := range builtins {
		want[name] = AgentConfig{Command: cmd}
	}
	for name, ac := range cfg.Agents {
		if ac.Command == "" {
			ac.Command = builtins[name]
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
		la := localAgent{Name: name, Path: path, Args: ac.Args}
		if _, ok := builtins[name]; ok {
			la.Version = probeVersion(path)
		}
		out[name] = la
	}
	return out
}

func probeVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, "--version").Output()
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

// Result 是 agent 回一条消息的最终结果，作为 done / failed 的 payload。
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

// runAgent 在 dir 里跑 agent 回一条消息，过程中调用 report 回报进度。m.Resume 不为空时续接 harness 的会话。
func runAgent(ctx context.Context, la localAgent, m protocol.Dispatch, dir string, report reportFn) Result {
	var pi protocol.PromptInput
	_ = json.Unmarshal(m.Input, &pi)
	switch la.Name {
	case "claude":
		if pi.Prompt == "" {
			return Result{Error: "input.prompt 为空"}
		}
		args := []string{"-p", pi.Prompt, "--output-format", "stream-json", "--verbose"}
		if m.Resume != "" {
			args = append(args, "--resume", m.Resume)
		}
		args = append(args, la.Args...)
		return withSession(runClaude(ctx, exec.CommandContext(ctx, la.Path, args...), dir, report), m.Resume)
	case "codex":
		if pi.Prompt == "" {
			return Result{Error: "input.prompt 为空"}
		}
		args := append([]string{"exec", "--json", "--skip-git-repo-check"}, la.Args...)
		if m.Resume != "" {
			args = append(args, "resume", m.Resume)
		}
		args = append(args, pi.Prompt)
		return withSession(runCodex(ctx, exec.CommandContext(ctx, la.Path, args...), dir, report), m.Resume)
	default:
		in, _ := json.Marshal(protocol.HarnessInput{SessionID: m.SessionID, Resume: m.Resume, Input: m.Input})
		return withSession(runHarness(ctx, exec.CommandContext(ctx, la.Path, la.Args...), in, dir, report), m.Resume)
	}
}

// withSession:agent 没报会话 id 时沿用续接的那个，免得服务那边丢掉。
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

// runClaude 解析 claude 的 stream-json:assistant 文本和工具调用作为进度,result 行作为结果。
func runClaude(ctx context.Context, cmd *exec.Cmd, dir string, report reportFn) Result {
	stdout, stderr, err := start(cmd, dir, nil)
	if err != nil {
		return Result{Error: err.Error()}
	}
	var errTail tail
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); scanLines(stderr, errTail.add) }()

	var res Result
	gotResult := false
	scanLines(stdout, func(line string) {
		var m struct {
			Type      string  `json:"type"`
			Subtype   string  `json:"subtype"`
			SessionID string  `json:"session_id"`
			Result    string  `json:"result"`
			IsError   bool    `json:"is_error"`
			CostUSD   float64 `json:"total_cost_usd"`
			Message   struct {
				Content []struct {
					Type  string          `json:"type"`
					Text  string          `json:"text"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			report(map[string]string{"text": truncate(line, 4000)})
			return
		}
		if m.SessionID != "" {
			res.SessionID = m.SessionID
		}
		switch m.Type {
		case "assistant":
			for _, c := range m.Message.Content {
				switch c.Type {
				case "text":
					if strings.TrimSpace(c.Text) != "" {
						report(map[string]string{"text": truncate(c.Text, 4000)})
					}
				case "tool_use":
					report(map[string]string{"text": "🔧 " + c.Name + " " + truncate(string(c.Input), 300)})
				}
			}
		case "result":
			gotResult = true
			res.OK = !m.IsError && (m.Subtype == "" || m.Subtype == "success")
			res.Output = m.Result
			res.CostUSD = m.CostUSD
			if !res.OK {
				res.Error = m.Subtype
			}
		}
	})
	wg.Wait()
	werr := cmd.Wait()
	if !gotResult {
		res.OK = false
		res.Error = exitError(ctx, werr, errTail.String())
	} else if werr != nil && res.OK {
		res.OK, res.Error = false, exitError(ctx, werr, errTail.String())
	}
	return res
}

// runCodex 解析 codex exec --json 的 JSONL:thread.started 给会话 id,agent 消息和命令作为进度，
// 最后一条 agent 消息作为结果输出,turn.completed 为成功。
func runCodex(ctx context.Context, cmd *exec.Cmd, dir string, report reportFn) Result {
	stdout, stderr, err := start(cmd, dir, nil)
	if err != nil {
		return Result{Error: err.Error()}
	}
	var errTail tail
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); scanLines(stderr, errTail.add) }()

	var res Result
	completed := false
	scanLines(stdout, func(line string) {
		var m struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Message  string `json:"message"`
			Error    struct {
				Message string `json:"message"`
			} `json:"error"`
			Item struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Command string `json:"command"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			if strings.TrimSpace(line) != "" {
				report(map[string]string{"text": truncate(line, 4000)})
			}
			return
		}
		switch m.Type {
		case "thread.started":
			res.SessionID = m.ThreadID
		case "item.completed":
			switch m.Item.Type {
			case "agent_message":
				res.Output = m.Item.Text
				report(map[string]string{"text": truncate(m.Item.Text, 4000)})
			case "command_execution":
				report(map[string]string{"text": "🔧 " + truncate(m.Item.Command, 300)})
			}
		case "turn.completed":
			completed = true
		case "turn.failed":
			res.Error = m.Error.Message
		case "error":
			report(map[string]string{"text": truncate(m.Message, 4000)})
			res.Error = m.Message
		}
	})
	wg.Wait()
	werr := cmd.Wait()
	if completed && werr == nil {
		res.OK, res.Error = true, ""
		return res
	}
	if res.Error == "" || werr != nil {
		msg := exitError(ctx, werr, errTail.String())
		if res.Error != "" {
			msg = res.Error + "; " + msg
		}
		res.Error = msg
	}
	return res
}

// runHarness 按自定义 harness 的约定跑:HarnessInput 写进 stdin,stdout 每行一个 JSON。
//
//	{"type":"progress", ...任意字段}         → 原样作为进度回报
//	{"type":"result","ok":true,"output":"…","session_id":"…"} → 最终结果
//
// 不是 JSON 的行当作文本进度。没输出 result 就按退出码判断成败。
func runHarness(ctx context.Context, cmd *exec.Cmd, input json.RawMessage, dir string, report reportFn) Result {
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
