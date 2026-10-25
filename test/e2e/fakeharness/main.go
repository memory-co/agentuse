// fakeharness 是 e2e 测试用的自研 harness:一个可执行文件，按命令行的样子扮演三种 harness,
// 不调用任何模型，输出可预测，方便断言。
//
//	fakeharness --version                    → 打印版本(daemon 探测用)
//	fakeharness -p PROMPT … [--resume ID]   → 扮演 claude(harness/claude/harness.yaml 的 stream-json)
//	fakeharness exec --json … [resume ID] PROMPT → 扮演 codex(harness/codex/harness.yaml 的 JSONL)
//	fakeharness(其它)                        → 自定义 harness:stdin 读 {session_id,resume,input},stdout 写 progress / result
//
// 回复内容都带上 prompt、续接的会话 id 和工作目录名，测试据此确认 daemon 传对了参数、在对的目录里跑。
// prompt 为 "fail" 时按各自的格式报失败。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// claude:-p PROMPT --output-format stream-json --verbose [--resume ID] [额外参数…]
func claude(args []string) int {
	prompt, resume := args[1], ""
	for i := 2; i < len(args)-1; i++ {
		if args[i] == "--resume" {
			resume = args[i+1]
		}
	}
	sid := resume
	if sid == "" {
		sid = "claude-" + cwd()
	}
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "thinking about " + prompt},
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "ls"}},
	}}})
	if prompt == "fail" {
		emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "session_id": sid})
		return 1
	}
	emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "session_id": sid,
		"result": reply("claude", prompt, resume), "total_cost_usd": 0.01})
	return 0
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
		SessionID string `json:"session_id"`
		Resume    string `json:"resume"`
		Input     struct {
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
	sid := in.Resume
	if sid == "" {
		sid = "custom-" + in.SessionID
	}
	emit(map[string]any{"type": "result", "ok": true, "output": reply("custom", in.Input.Prompt, in.Resume), "session_id": sid})
	return 0
}
