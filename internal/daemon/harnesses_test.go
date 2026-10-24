package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/harness"
	"github.com/memory-co/agentuse/internal/protocol"
)

// 假 codex:把参数写进 args.txt,按 codex exec --json 的格式输出 JSONL。
const fakeCodex = `#!/bin/sh
echo "$@" > args.txt
echo '{"type":"thread.started","thread_id":"th-1"}'
echo '{"type":"turn.started"}'
echo '{"type":"item.completed","item":{"type":"command_execution","command":"ls"}}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"done!"}}'
echo '{"type":"turn.completed"}'
`

func TestRunCodexResume(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	os.WriteFile(bin, []byte(fakeCodex), 0o755)
	sp, _ := harness.Get("codex")
	la := localHarness{Name: "codex", Path: bin, Args: []string{"-s", "workspace-write"}, Spec: &sp}
	var progress []string
	report := func(p any) { b, _ := json.Marshal(p); progress = append(progress, string(b)) }

	res := runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, report)
	if !res.OK || res.Output != "done!" || res.SessionID != "th-1" || len(progress) != 2 {
		t.Fatalf("first: %+v %v", res, progress)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); got != "exec --json --skip-git-repo-check -s workspace-write hi" {
		t.Fatalf("args: %q", got)
	}

	res = runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Resume: "th-1", Input: json.RawMessage(`{"prompt":"again"}`)}, dir, report)
	args, _ = os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); !res.OK || got != "exec --json --skip-git-repo-check -s workspace-write resume th-1 again" {
		t.Fatalf("resume: %+v %q", res, got)
	}
}

// 假 claude:按 stream-json 输出,result 行给结果和会话 id。
const fakeClaude = `#!/bin/sh
echo "$@" > args.txt
echo '{"type":"system","subtype":"init","session_id":"s-1"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok!","session_id":"s-1"}'
`

func TestRunClaudeResume(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(fakeClaude), 0o755)
	sp, _ := harness.Get("claude")
	la := localHarness{Name: "claude", Path: bin, Spec: &sp}
	res := runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Resume: "s-1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, func(any) {})
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); !res.OK || res.Output != "ok!" || res.SessionID != "s-1" || got != "-p hi --output-format stream-json --verbose --resume s-1" {
		t.Fatalf("%+v %q", res, got)
	}

	// 没有 result 行 → 失败
	os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"type\":\"system\"}'\n"), 0o755)
	if res = runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, func(any) {}); res.OK || res.Error == "" {
		t.Fatalf("no result: %+v", res)
	}
}
