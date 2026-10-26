package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	report := func(_ string, p any) { b, _ := json.Marshal(p); progress = append(progress, string(b)) }

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

// 假 claude:从 stdin 读一行消息，按 stream-json 输出,result 行给结果和会话 id;
// 之后像真的 claude 一样等 stdin 关掉才退出。
const fakeClaude = `#!/bin/sh
echo "$@" > args.txt
read line; echo "$line" > stdin.txt
echo '{"type":"system","subtype":"init","session_id":"s-1"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok!","session_id":"s-1"}'
cat >/dev/null
`

func TestRunClaudeResume(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(fakeClaude), 0o755)
	sp, _ := harness.Get("claude")
	la := localHarness{Name: "claude", Path: bin, Spec: &sp}
	res := runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Resume: "s-1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, func(string, any) {})
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); !res.OK || res.Output != "ok!" || res.SessionID != "s-1" ||
		got != "-p --input-format stream-json --output-format stream-json --verbose --permission-mode bypassPermissions --disallowedTools AskUserQuestion --resume s-1" {
		t.Fatalf("%+v %q", res, got)
	}
	in, _ := os.ReadFile(filepath.Join(dir, "stdin.txt"))
	if got := strings.TrimSpace(string(in)); got != `{"message":{"content":"hi","role":"user"},"type":"user"}` {
		t.Fatalf("stdin: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "agentuse", "SKILL.md")); err != nil {
		t.Fatalf("skill 没拷进会话目录: %v", err)
	}

	// 没有 result 行 → 失败
	os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"type\":\"system\"}'\n"), 0o755)
	if res = runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, func(string, any) {}); res.OK || res.Error == "" {
		t.Fatalf("no result: %+v", res)
	}
}

func custom(t *testing.T, script string) localHarness {
	t.Helper()
	return localHarness{Name: "mine", Path: fakeBin(t, t.TempDir(), "mine", script)}
}

func dispatch(prompt, resume string) protocol.Dispatch {
	in, _ := json.Marshal(protocol.PromptInput{Prompt: prompt})
	return protocol.Dispatch{ID: 1, SessionID: "ss_1", Harness: "mine", Resume: resume, Input: in}
}

func TestRunCustomHarness(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, c := range []struct {
		name, script string
		want         Result
		progress     []string
	}{
		{"result", `read in; echo '{"type":"progress","n":1}'; echo "$in" | sed 's/^/got /'; echo '{"type":"plan","steps":[{"text":"a","status":"pending"}]}'; echo '{"type":"result","ok":true,"output":"done","session_id":"h-2","cost_usd":0.5}'`,
			Result{OK: true, Output: "done", SessionID: "h-2", CostUSD: 0.5},
			[]string{`{"type":"progress","n":1}`, `{"text":"got {\"session_id\":\"ss_1\",\"resume\":\"h-1\",\"input\":{\"prompt\":\"hi\"}}"}`, `plan {"steps":[{"text":"a","status":"pending"}]}`}},
		// 没有 result:退出码 0 就成功，文本输出当结果;没报会话 id 时沿用续接的
		{"plain text", "cat >/dev/null; echo line1; echo; echo line2",
			Result{OK: true, Output: "line1\nline2", SessionID: "h-1"}, []string{`{"text":"line1"}`, `{"text":"line2"}`}},
		{"exit code", "cat >/dev/null; echo partial; echo oops >&2; exit 3",
			Result{Output: "partial", Error: "exit status 3: oops", SessionID: "h-1"}, []string{`{"text":"partial"}`}},
		// 报了成功但退出码非 0:按失败算
		{"result but exit 1", `cat >/dev/null; echo '{"type":"result","ok":true,"output":"x"}'; exit 1`,
			Result{Output: "x", Error: "exit status 1", SessionID: "h-1"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			var progress []string
			res := runHarness(ctx, custom(t, c.script), dispatch("hi", "h-1"), dir, func(kind string, p any) {
				b, _ := json.Marshal(p)
				if kind != "progress" {
					b = append([]byte(kind+" "), b...)
				}
				progress = append(progress, string(b))
			})
			if res != c.want || !reflect.DeepEqual(progress, c.progress) {
				t.Fatalf("got %+v %q\nwant %+v %q", res, progress, c.want, c.progress)
			}
		})
	}

	// 跑在会话目录里
	res := runHarness(ctx, custom(t, "cat >/dev/null; pwd"), dispatch("hi", ""), dir, func(string, any) {})
	if wd, _ := filepath.EvalSymlinks(dir); !res.OK || (res.Output != dir && res.Output != wd) {
		t.Fatalf("cwd: %+v", res)
	}
	// 可执行文件没了
	if res := runHarness(ctx, localHarness{Name: "x", Path: filepath.Join(dir, "missing")}, dispatch("hi", ""), dir, func(string, any) {}); res.OK || res.Error == "" {
		t.Fatalf("missing binary: %+v", res)
	}
}

func TestRunCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() { <-started; cancel() }()
	// exec:让 sleep 顶替 shell。harness 的子进程如果还拿着 stdout,取消要等它退出(见 PR 说明)
	res := runHarness(ctx, custom(t, "cat >/dev/null; echo started; exec sleep 10"), dispatch("hi", ""), t.TempDir(), func(string, any) { close(started) })
	if res.OK || res.Error != "已取消" {
		t.Fatalf("%+v", res)
	}
}

func TestRunSpecFinish(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	codex, _ := harness.Get("codex")
	claude, _ := harness.Get("claude")
	run := func(sp harness.Spec, script string) Result {
		la := localHarness{Name: sp.Name, Path: fakeBin(t, t.TempDir(), sp.Name, script), Spec: &sp}
		return runHarness(ctx, la, dispatch("hi", ""), dir, func(string, any) {})
	}

	// codex(require_ok):turn.failed 的错误 + 退出码都报出来
	res := run(codex, `echo '{"type":"turn.failed","error":{"message":"boom"}}'; echo bad >&2; exit 1`)
	if res.OK || res.Error != "boom; exit status 1: bad" {
		t.Fatalf("codex failed: %+v", res)
	}
	// 没看到 turn.completed 也算失败
	if res = run(codex, `echo '{"type":"item.completed","item":{"type":"agent_message","text":"x"}}'`); res.OK || res.Error != "进程没有给出结果" {
		t.Fatalf("codex no ok: %+v", res)
	}
	// claude(require_result):result 说成功但退出码非 0
	if res = run(claude, `echo '{"type":"result","subtype":"success","is_error":false,"result":"x"}'; exit 2`); res.OK || res.Error != "exit status 2" {
		t.Fatalf("claude exit 2: %+v", res)
	}
	// result 说失败:用 result 里的错误
	if res = run(claude, `echo '{"type":"result","subtype":"error_max_turns","is_error":true}'; exit 1`); res.OK || res.Error != "error_max_turns" {
		t.Fatalf("claude error result: %+v", res)
	}
	// 空 prompt 不起进程
	la := localHarness{Name: "claude", Path: "/nonexistent", Spec: &claude}
	if res = runHarness(ctx, la, protocol.Dispatch{Input: json.RawMessage(`{}`)}, dir, func(string, any) {}); res.OK || !strings.Contains(res.Error, "prompt") {
		t.Fatalf("empty prompt: %+v", res)
	}
	// 都不写 finish:按退出码
	var plain harness.Spec
	plain.Name, plain.Run.New = "plain", []string{"{prompt}"}
	if res = run(plain, `echo "$1"`); !res.OK || res.Error != "" {
		t.Fatalf("plain ok: %+v", res)
	}
	if res = run(plain, `exit 4`); res.OK || res.Error != "exit status 4" {
		t.Fatalf("plain fail: %+v", res)
	}
}

func TestHelpers(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	if expandHome("~/a") != "/home/x/a" || expandHome("/abs") != "/abs" || expandHome("~user") != "~user" {
		t.Fatal("expandHome")
	}
	if truncate("abcd", 2) != "ab…" || truncate("ab", 2) != "ab" {
		t.Fatal("truncate")
	}
	if withSession(Result{}, "r").SessionID != "r" || withSession(Result{SessionID: "s"}, "r").SessionID != "s" {
		t.Fatal("withSession")
	}
	var tl tail
	tl.add(strings.Repeat("x", maxOutput))
	tl.add("end")
	if s := tl.String(); len(s) > maxOutput || !strings.HasSuffix(s, "\nend") {
		t.Fatalf("tail keeps the last %d bytes: %d", maxOutput, len(s))
	}
}
