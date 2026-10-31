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

func TestWorkInstructions(t *testing.T) {
	dir := t.TempDir()
	sp, _ := harness.Get("codex")
	if sp.Instructions != "AGENTS.md" {
		t.Fatalf("codex instructions: %q", sp.Instructions)
	}
	run := func(m protocol.Dispatch) string {
		t.Helper()
		m.SessionID, m.Input = "ss_1", json.RawMessage(`{"prompt":"hi"}`)
		la := codexTestHarness(t, "instructions")
		la.Env = append(la.Env, workEnv("http://srv", m)...)
		la.Spec = &sp
		if res := runHarness(context.Background(), la, m, dir, nil, func(string, any) {}); !res.OK {
			t.Fatalf("run: %+v", res)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "seen.txt"))
		return string(b)
	}

	// 线程：只有工作的说明，没有令牌
	if got := run(protocol.Dispatch{Instructions: "先读 README"}); got != "先读 README\ntoken=\n" {
		t.Fatalf("thread: %q", got)
	}
	// 协调者：agentuse work 的用法在最前面，工作的说明接在后面，带令牌
	if got := run(protocol.Dispatch{Instructions: "先读 README", WorkToken: "wt_x"}); !strings.HasPrefix(got, "# 你是这个工作的协调者：只派活，不干活") || !strings.Contains(got, "agentuse work new") || !strings.HasSuffix(got, "# 工作说明\n\n先读 README\ntoken=wt_x\n") {
		t.Fatalf("coordinator: %q", got)
	}
	// 不在工作里：说明文件删掉
	if got := run(protocol.Dispatch{}); got != "token=\n" {
		t.Fatalf("plain: %q", got)
	}
}

func TestWorkCustomHarness(t *testing.T) {
	m := dispatch("hi")
	m.Instructions, m.WorkToken = "说明", "wt_y"
	la := custom(t, `sed 's/^/in /'; echo "$AGENTUSE_SERVER $AGENTUSE_WORK"`)
	la.Env = workEnv("http://srv", m)
	res := runHarness(context.Background(), la, m, t.TempDir(), nil, func(string, any) {})
	var in protocol.HarnessInput
	first, last, _ := strings.Cut(res.Output, "\n")
	if err := json.Unmarshal([]byte(strings.TrimPrefix(first, "in ")), &in); err != nil || !strings.HasPrefix(in.Instructions, "# 你是这个工作的协调者") || !strings.HasSuffix(in.Instructions, "# 工作说明\n\n说明") || last != "http://srv wt_y" {
		t.Fatalf("custom: %+v %q", in, res.Output)
	}
}
