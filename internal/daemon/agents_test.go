package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	la := localAgent{Name: "codex", Path: bin, Args: []string{"-s", "workspace-write"}}
	var progress []string
	report := func(p any) { b, _ := json.Marshal(p); progress = append(progress, string(b)) }

	res := runAgent(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, report)
	if !res.OK || res.Output != "done!" || res.SessionID != "th-1" || len(progress) != 2 {
		t.Fatalf("first: %+v %v", res, progress)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); got != "exec --json --skip-git-repo-check -s workspace-write hi" {
		t.Fatalf("args: %q", got)
	}

	res = runAgent(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Resume: "th-1", Input: json.RawMessage(`{"prompt":"again"}`)}, dir, report)
	args, _ = os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); !res.OK || got != "exec --json --skip-git-repo-check -s workspace-write resume th-1 again" {
		t.Fatalf("resume: %+v %q", res, got)
	}
}
