// fakeharness 是 e2e 测试用的自研 harness:一个可执行文件，按命令行的样子扮演三种 harness,
// 不调用任何模型，输出可预测，方便断言。
//
//	fakeharness --version                    → 打印版本(daemon 探测用)
//	fakeharness -p … [--resume ID]          → 扮演 claude(harness/claude/harness.yaml 的 stream-json,prompt 从 stdin 读)
//	fakeharness exec --json … [resume ID] PROMPT → 扮演 codex(harness/codex/harness.yaml 的 JSONL)
//	fakeharness(其它)                        → 自定义 harness:stdin 读 {session_id,resume,input},stdout 写 progress / result
//
// 回复内容都带上 prompt、续接的会话 id 和工作目录名，测试据此确认 daemon 传对了参数、在对的目录里跑。
// prompt 为 "fail" 时按各自的格式报失败;claude 模式下 "ask" 会用 AskUserQuestion 提问，等 daemon 回答;
// "spawn <agent> <要做的事>" 像工作的协调者一样用 agentuse work new 开线程(docs/v2/designs/work.md)。
// claude 模式下会话目录里有 CLAUDE.md(工作的说明)时，回复带上它的第一行。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const Version = "fakeharness 1.0.0"

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 1 && args[0] == "--version":
		fmt.Println(Version)
	case len(args) > 0 && args[0] == "-p":
		os.Exit(claude(args))
	case len(args) > 0 && args[0] == "exec":
		os.Exit(codex(args))
	default:
		os.Exit(custom())
	}
}

func emit(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func cwd() string {
	d, _ := os.Getwd()
	return filepath.Base(d)
}

// reply 是三种模式共用的回复文本。
func reply(mode, prompt, resume string) string {
	return fmt.Sprintf("%s: %s (resume=%s, cwd=%s)", mode, prompt, resume, cwd())
}

// claude:-p --input-format stream-json … [--resume ID] [额外参数…],stdin 一行 {"type":"user",…}。
// 和真的 claude 一样，回完以后等 stdin 关掉才退出;会话目录里没有 agentuse skill 就报错。
func claude(args []string) int {
	resume := ""
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "--resume" {
			resume = args[i+1]
		}
	}
	stdin := bufio.NewReader(os.Stdin)
	// 第一行是 daemon 注册 hook 的 control_request(插话用)
	if line, _ := stdin.ReadBytes('\n'); !strings.Contains(string(line), `"initialize"`) {
		fmt.Fprintln(os.Stderr, "missing initialize:", string(line))
		return 2
	}
	line, _ := stdin.ReadBytes('\n')
	var in struct {
		Type    string `json:"type"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &in); err != nil || in.Type != "user" {
		fmt.Fprintln(os.Stderr, "bad stdin:", string(line))
		return 2
	}
	if _, err := os.Stat(filepath.Join(".claude", "skills", "agentuse", "SKILL.md")); err != nil {
		fmt.Fprintln(os.Stderr, "missing skill:", err)
		return 2
	}
	defer io.Copy(io.Discard, stdin)
	prompt := in.Message.Content
	sid := resume
	if sid == "" {
		sid = "claude-" + cwd()
	}
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "thinking about " + prompt},
		map[string]any{"type": "tool_use", "name": "TodoWrite", "input": map[string]any{"todos": []any{
			map[string]any{"content": "读 " + prompt, "status": "completed"},
			map[string]any{"content": "回复", "status": "in_progress"},
		}}},
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "ls"}},
	}}})
	if prompt == "fail" {
		emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "session_id": sid})
		return 1
	}
	out := reply("claude", prompt, resume)
	if b, err := os.ReadFile("CLAUDE.md"); err == nil {
		first, _, _ := strings.Cut(string(b), "\n")
		if first != "" {
			out += " claude.md=" + first
		}
	}
	if f := strings.SplitN(prompt, " ", 3); len(f) == 3 && f[0] == "spawn" {
		b, err := exec.Command("agentuse", "work", "new", "--agents", f[1], f[2]).CombinedOutput()
		out += " spawned=" + strings.TrimSpace(string(b))
		if err != nil {
			out += " err=" + err.Error()
		}
	}
	if prompt == "wait-steer" {
		// 每 100ms 发一次 PreToolUse 回调，直到 daemon 塞来插话，最多 10 秒
		for i := 0; i < 100; i++ {
			if extra := preToolUse(stdin, i); extra != "" {
				out += " +" + extra[strings.LastIndex(extra, "\n")+1:]
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if prompt == "ask" {
		if !contains(args, "--permission-prompt-tool") {
			fmt.Fprintln(os.Stderr, "AskUserQuestion 没打开")
			return 2
		}
		out += " " + askUser(stdin)
	}
	emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "session_id": sid,
		"result": out, "total_cost_usd": 0.01})
	return 0
}

func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// askUser 像真的 claude 一样发一条 can_use_tool(AskUserQuestion),一直等到 daemon 回复。
// 返回 "answer=<答案>" 或 "denied=<原因>"。
func askUser(stdin *bufio.Reader) string {
	q := "用哪种语言？"
	emit(map[string]any{"type": "control_request", "request_id": "ask-1", "request": map[string]any{
		"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "tool_use_id": "toolu_ask1", "requires_user_interaction": true,
		"input": map[string]any{"questions": []any{map[string]any{"question": q, "header": "语言", "multiSelect": false,
			"options": []any{map[string]any{"label": "Go"}, map[string]any{"label": "Python"}}}}}}})
	line, _ := stdin.ReadBytes('\n')
	var resp struct {
		Response struct {
			RequestID string `json:"request_id"`
			Response  struct {
				Behavior     string `json:"behavior"`
				Message      string `json:"message"`
				UpdatedInput struct {
					Answers map[string]string `json:"answers"`
				} `json:"updatedInput"`
			} `json:"response"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &resp) != nil || resp.Response.RequestID != "ask-1" {
		return "bad-response=" + strings.TrimSpace(string(line))
	}
	if r := resp.Response.Response; r.Behavior == "allow" {
		return "answer=" + r.UpdatedInput.Answers[q]
	}
	return "denied=" + resp.Response.Response.Message
}

// preToolUse 发一次 PreToolUse hook 回调，返回 daemon 塞来的 additionalContext。
func preToolUse(stdin *bufio.Reader, n int) string {
	id := fmt.Sprintf("pre-%d", n)
	emit(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{
		"subtype": "hook_callback", "callback_id": "agentuse-pre", "input": map[string]any{}}})
	line, _ := stdin.ReadBytes('\n')
	var resp struct {
		Response struct {
			RequestID string `json:"request_id"`
			Response  struct {
				HookSpecificOutput struct {
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			} `json:"response"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &resp) != nil || resp.Response.RequestID != id {
		return ""
	}
	return resp.Response.Response.HookSpecificOutput.AdditionalContext
}

// codex:exec --json --skip-git-repo-check [额外参数…] [resume ID] PROMPT
func codex(args []string) int {
	prompt, resume := args[len(args)-1], ""
	if n := len(args); n >= 3 && args[n-3] == "resume" {
		resume = args[n-2]
	}
	if resume == "" {
		emit(map[string]any{"type": "thread.started", "thread_id": "codex-" + cwd()})
	}
	emit(map[string]any{"type": "turn.started"})
	emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "command": "ls"}})
	if prompt == "slow" {
		time.Sleep(time.Second) // 给测试留出插话的时间
	}
	if prompt == "fail" {
		emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": "fake failure"}})
		return 1
	}
	emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": reply("codex", prompt, resume)}})
	emit(map[string]any{"type": "turn.completed"})
	return 0
}

// custom:自定义 harness 的 stdin / stdout 约定(docs/v2/tables/harnesses.md)。
func custom() int {
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		fmt.Fprintln(os.Stderr, "no input:", err)
		return 2
	}
	var in struct {
		Attachments []struct {
			Path     string `json:"path"`
			Filename string `json:"filename"`
		} `json:"attachments"`
		RunID           string          `json:"run_id"`
		AgentID         string          `json:"agent_id"`
		SessionMessages json.RawMessage `json:"session_messages"`
		TaskMessageIDs  []int64         `json:"task_message_ids"`
		SessionID       string          `json:"session_id"`
		Resume          string          `json:"resume"`
		Input           struct {
			Prompt string `json:"prompt"`
		} `json:"input"`
	}
	if err := json.Unmarshal(line, &in); err != nil {
		fmt.Fprintln(os.Stderr, "bad input:", err)
		return 2
	}
	emit(map[string]any{"type": "progress", "text": "working on " + in.Input.Prompt})
	fmt.Println("plain text progress")
	if in.Input.Prompt == "fail" {
		fmt.Fprintln(os.Stderr, "fake failure")
		return 3
	}
	if strings.HasPrefix(in.Input.Prompt, "multi:") {
		if in.RunID == "" || in.Resume != "" || os.Getenv("AGENTUSE_RUN") != in.RunID {
			fmt.Fprintln(os.Stderr, "missing fresh run context")
			return 2
		}
		if in.Input.Prompt == "multi:hold" {
			for i := 0; i < 600; i++ {
				if _, err := os.Stat("release"); err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		cmd := exec.Command("agentuse", "session", "read", "--json")
		live, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintln(os.Stderr, string(live), err)
			return 2
		}
		var state map[string]any
		if err = json.Unmarshal(live, &state); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		out, _ := json.Marshal(map[string]any{"agent_id": in.AgentID, "run_id": in.RunID, "resume": in.Resume, "history": in.SessionMessages, "task_ids": in.TaskMessageIDs, "live": state["messages"]})
		emit(map[string]any{"type": "result", "ok": true, "output": string(out), "session_id": "native-" + in.RunID})
		return 0
	}
	if in.Input.Prompt == "attachments" {
		if len(in.Attachments) != 1 {
			fmt.Fprintln(os.Stderr, "missing attachment context")
			return 2
		}
		b, err := os.ReadFile(in.Attachments[0].Path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if err = os.WriteFile("report.txt", append([]byte("result:"), b...), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		cmd := exec.Command("agentuse", "attachment", "upload", "report.txt", "--json")
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintln(os.Stderr, string(out), err)
			return 2
		}
		emit(map[string]any{"type": "result", "ok": true, "output": string(out), "session_id": "custom-" + in.SessionID})
		return 0
	}
	sid := in.Resume
	if sid == "" {
		sid = "custom-" + in.SessionID
	}
	emit(map[string]any{"type": "result", "ok": true, "output": reply("custom", in.Input.Prompt, in.Resume), "session_id": sid})
	return 0
}
