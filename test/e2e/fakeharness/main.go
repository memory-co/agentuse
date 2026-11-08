// fakeharness provides deterministic native app-server and stream-json backends
// for the independent harnessctl packages, plus a custom worker wrapped by
// harnessctl/tests/fake_adapter.py. It never calls a model.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	case len(args) > 0 && args[0] == "app-server":
		os.Exit(codexAppServer())
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
// Keep the native connection open for subsequent turns.
func claude(args []string) int {
	resume := ""
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "--resume" {
			resume = args[i+1]
		}
	}
	stdin := bufio.NewReader(os.Stdin)
	// The adapter initializes the native stream protocol before sending input.
	line, _ := stdin.ReadBytes('\n')
	if !strings.Contains(string(line), `"initialize"`) {
		fmt.Fprintln(os.Stderr, "missing initialize:", string(line))
		return 2
	}
	var init map[string]any
	json.Unmarshal(line, &init)
	emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": init["request_id"], "response": map[string]any{}}})
	return claudeTurns(args, resume, stdin)
}
func claudeTurns(args []string, resume string, stdin *bufio.Reader) int {
	for {
		line, err := stdin.ReadBytes('\n')
		if err != nil {
			return 0
		}
		var in struct {
			Type    string `json:"type"`
			UUID    string `json:"uuid"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &in); err != nil || in.Type != "user" {
			fmt.Fprintln(os.Stderr, "bad stdin:", string(line))
			return 2
		}
		emit(map[string]any{"type": "command_lifecycle", "command_uuid": in.UUID, "state": "queued"})
		emit(map[string]any{"type": "command_lifecycle", "command_uuid": in.UUID, "state": "started"})
		// Adapter instructions are now passed with the input, without mutating cwd.
		instructionText := in.Message.Content
		if parts := strings.SplitN(in.Message.Content, "\n\n# 本次消息\n", 2); len(parts) == 2 {
			in.Message.Content = parts[1]
		}

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
		if prompt == "context-image" {
			var err error
			out, err = contextProof(false)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
		}
		if instructionText != in.Message.Content {
			first, _, _ := strings.Cut(instructionText, "\n")
			if first != "" {
				out += " claude.md=" + first
			}
		}
		if f := strings.SplitN(prompt, " ", 3); len(f) == 3 && f[0] == "spawn" {
			b, err := exec.Command("context", "work", "new", "--agents", f[1], f[2]).CombinedOutput()
			out += " spawned=" + strings.TrimSpace(string(b))
			if err != nil {
				out += " err=" + err.Error()
			}
		}
		if prompt == "wait-steer" {
			time.Sleep(1500 * time.Millisecond)
		}
		if prompt == "ask" {
			if !contains(args, "--permission-prompt-tool") {
				fmt.Fprintln(os.Stderr, "AskUserQuestion 没打开")
				return 2
			}
			out += " " + askUser(stdin)
		}
		emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "session_id": sid,
			"result": out, "total_cost_usd": 0.01, "user_message_uuids": []string{in.UUID}})
		resume = sid
	}
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

// custom:自定义 harness 的 stdin / stdout 约定(designs/v2/tables/harnesses.md)。
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
	if strings.HasPrefix(in.Input.Prompt, "context-image") {
		out, err := contextProof(in.Input.Prompt == "context-image-hold")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		emit(map[string]any{"type": "result", "ok": true, "output": out})
		return 0
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
		cmd := exec.Command("context", "input")
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
		out, _ := json.Marshal(map[string]any{"agent_id": in.AgentID, "run_id": in.RunID, "resume": in.Resume, "history": in.SessionMessages, "task_ids": in.TaskMessageIDs, "live": state["inputs"]})
		emit(map[string]any{"type": "result", "ok": true, "output": string(out), "session_id": "native-" + in.RunID})
		return 0
	}
	if in.Input.Prompt == "ledger" {
		emit(map[string]any{"type": "result", "ok": true, "output": "ledger result", "usage": map[string]int64{"input_tokens": 1234, "output_tokens": 56, "cache_read_tokens": 100, "cache_write_tokens": 20}, "cost_usd": 0.0123})
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
