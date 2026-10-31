package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestRunCodexFresh(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	os.WriteFile(bin, []byte(fakeCodex), 0o755)
	sp, _ := harness.Get("codex")
	la := localHarness{Name: "codex", Path: bin, Args: []string{"-s", "workspace-write"}, Spec: &sp}
	var progress []string
	report := func(_ string, p any) { b, _ := json.Marshal(p); progress = append(progress, string(b)) }

	res := runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, nil, report)
	if !res.OK || res.Output != "done!" || res.SessionID != "th-1" || len(progress) != 2 {
		t.Fatalf("first: %+v %v", res, progress)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); got != "exec --json --skip-git-repo-check -c sandbox_mode=workspace-write -c sandbox_workspace_write.network_access=true -s workspace-write hi" {
		t.Fatalf("args: %q", got)
	}

	res = runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"again"}`)}, dir, nil, report)
	args, _ = os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); !res.OK || got != "exec --json --skip-git-repo-check -c sandbox_mode=workspace-write -c sandbox_workspace_write.network_access=true -s workspace-write again" {
		t.Fatalf("resume: %+v %q", res, got)
	}
}

// 假 claude:从 stdin 读一行消息，按 stream-json 输出,result 行给结果和会话 id;
// 之后像真的 claude 一样等 stdin 关掉才退出。
const fakeClaude = `#!/bin/sh
echo "$@" > args.txt
read init; echo "$init" > init.txt
read line; echo "$line" > stdin.txt
echo '{"type":"system","subtype":"init","session_id":"s-1"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok!","session_id":"s-1"}'
cat >/dev/null
`

func TestRunClaudeFresh(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(fakeClaude), 0o755)
	sp, _ := harness.Get("claude")
	la := localHarness{Name: "claude", Path: bin, Spec: &sp}
	res := runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, nil, func(string, any) {})
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if got := strings.TrimSpace(string(args)); !res.OK || res.Output != "ok!" || res.SessionID != "s-1" ||
		got != "-p --input-format stream-json --output-format stream-json --verbose --permission-mode bypassPermissions --permission-prompt-tool stdio" {
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
	if res = runHarness(context.Background(), la, protocol.Dispatch{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, nil, func(string, any) {}); res.OK || res.Error == "" {
		t.Fatalf("no result: %+v", res)
	}
}

func custom(t *testing.T, script string) localHarness {
	t.Helper()
	return localHarness{Name: "mine", Path: fakeBin(t, t.TempDir(), "mine", script)}
}

func dispatch(prompt string) protocol.Dispatch {
	in, _ := json.Marshal(protocol.PromptInput{Prompt: prompt})
	return protocol.Dispatch{ID: 1, SessionID: "ss_1", Harness: "mine", Input: in}
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
			[]string{`{"type":"progress","n":1}`, `{"text":"got {\"session_id\":\"ss_1\",\"input\":{\"prompt\":\"hi\"}}"}`, `plan {"steps":[{"text":"a","status":"pending"}]}`}},
		// 没有 result:退出码 0 就成功，文本输出当结果;没报会话 id 时沿用续接的
		{"plain text", "cat >/dev/null; echo line1; echo; echo line2",
			Result{OK: true, Output: "line1\nline2"}, []string{`{"text":"line1"}`, `{"text":"line2"}`}},
		{"exit code", "cat >/dev/null; echo partial; echo oops >&2; exit 3",
			Result{Output: "partial", Error: "exit status 3: oops"}, []string{`{"text":"partial"}`}},
		// 报了成功但退出码非 0:按失败算
		{"result but exit 1", `cat >/dev/null; echo '{"type":"result","ok":true,"output":"x"}'; exit 1`,
			Result{Output: "x", Error: "exit status 1"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			var progress []string
			res := runHarness(ctx, custom(t, c.script), dispatch("hi"), dir, nil, func(kind string, p any) {
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
	res := runHarness(ctx, custom(t, "cat >/dev/null; pwd"), dispatch("hi"), dir, nil, func(string, any) {})
	if wd, _ := filepath.EvalSymlinks(dir); !res.OK || (res.Output != dir && res.Output != wd) {
		t.Fatalf("cwd: %+v", res)
	}
	// 可执行文件没了
	if res := runHarness(ctx, localHarness{Name: "x", Path: filepath.Join(dir, "missing")}, dispatch("hi"), dir, nil, func(string, any) {}); res.OK || res.Error == "" {
		t.Fatalf("missing binary: %+v", res)
	}
}

func TestRunCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() { <-started; cancel() }()
	// exec:让 sleep 顶替 shell。harness 的子进程如果还拿着 stdout,取消要等它退出(见 PR 说明)
	res := runHarness(ctx, custom(t, "cat >/dev/null; echo started; exec sleep 10"), dispatch("hi"), t.TempDir(), nil, func(string, any) { close(started) })
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
		return runHarness(ctx, la, dispatch("hi"), dir, nil, func(string, any) {})
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
	if res = runHarness(ctx, la, protocol.Dispatch{Input: json.RawMessage(`{}`)}, dir, nil, func(string, any) {}); res.OK || !strings.Contains(res.Error, "prompt") {
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
	var tl tail
	tl.add(strings.Repeat("x", maxOutput))
	tl.add("end")
	if s := tl.String(); len(s) > maxOutput || !strings.HasSuffix(s, "\nend") {
		t.Fatalf("tail keeps the last %d bytes: %d", maxOutput, len(s))
	}
}

// 假 claude:先后发 PreToolUse、Stop 两个 hook 回调，把 daemon 回的 control_response 记下来。
const fakeClaudeHooks = `#!/bin/sh
read init; echo "$init" > init.txt
read line
echo '{"type":"system","subtype":"init","session_id":"s-1"}'
echo '{"type":"control_request","request_id":"r1","request":{"subtype":"hook_callback","callback_id":"agentuse-pre","input":{}}}'
read -r resp; printf "%s\n" "$resp" > pre.txt
echo '{"type":"control_request","request_id":"r2","request":{"subtype":"hook_callback","callback_id":"agentuse-stop","input":{}}}'
read -r resp; printf "%s\n" "$resp" > stop.txt
echo '{"type":"control_request","request_id":"r3","request":{"subtype":"something_else"}}'
read -r resp; printf "%s\n" "$resp" > other.txt
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok!","session_id":"s-1"}'
cat >/dev/null
`

func TestRunClaudeSteer(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(fakeClaudeHooks), 0o755)
	sp, _ := harness.Get("claude")
	la := localHarness{Name: "claude", Path: bin, Spec: &sp}
	steers := &steerQueue{}
	steers.push(protocol.Steer{ID: 7, MessageID: 1, Prompt: "顺便改 CHANGELOG"})
	steers.push(protocol.Steer{ID: 8, MessageID: 1, Prompt: "还有 README"})
	var reports []string
	res := runHarness(context.Background(), la, protocol.Dispatch{ID: 1, SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)}, dir, &inbox{steers: steers}, func(kind string, p any) {
		if kind == "steered" {
			b, _ := json.Marshal(p)
			reports = append(reports, string(b))
		}
	})
	if !res.OK || res.Output != "ok!" {
		t.Fatalf("%+v", res)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b))
	}
	if init := read("init.txt"); !strings.Contains(init, `"subtype":"initialize"`) || !strings.Contains(init, `"agentuse-pre"`) || !strings.Contains(init, `"agentuse-stop"`) {
		t.Fatalf("init: %s", init)
	}
	want := `{"response":{"request_id":"r1","response":{"hookSpecificOutput":{"additionalContext":"[用户插话] 用户在你工作时补充了下面的话。这是对原任务的补充，不是替换原任务：\n顺便改 CHANGELOG\n\n还有 README","hookEventName":"PreToolUse"}},"subtype":"success"},"type":"control_response"}`
	if got := read("pre.txt"); got != want {
		t.Fatalf("pre:\n%s\n%s", got, want)
	}
	// 插话已经在 PreToolUse 送出去了,Stop 不再拦
	if got := read("stop.txt"); got != `{"response":{"request_id":"r2","response":{},"subtype":"success"},"type":"control_response"}` {
		t.Fatalf("stop: %s", got)
	}
	if got := read("other.txt"); got != `{"response":{"request_id":"r3","response":{},"subtype":"success"},"type":"control_response"}` {
		t.Fatalf("other: %s", got)
	}
	if !reflect.DeepEqual(reports, []string{`{"steer_id":7}`, `{"steer_id":8}`}) {
		t.Fatalf("steered: %v", reports)
	}
}

func TestClaudeControlStop(t *testing.T) {
	var buf strings.Builder
	w := &stdinWriter{w: nopCloser{&buf}}
	steers := &steerQueue{}
	steers.push(protocol.Steer{ID: 1, Prompt: "再加个测试"})
	var n int
	if !claudeControl(`{"type":"control_request","request_id":"r","request":{"subtype":"hook_callback","callback_id":"agentuse-stop"}}`, w, &inbox{steers: steers}, func(string, any) { n++ }) {
		t.Fatal("应该处理 control_request")
	}
	if !strings.Contains(buf.String(), `"decision":"block"`) || !strings.Contains(buf.String(), "再加个测试") || n != 1 {
		t.Fatalf("stop: %s %d", buf.String(), n)
	}
	if claudeControl(`{"type":"assistant"}`, w, &inbox{steers: steers}, func(string, any) {}) {
		t.Fatal("不是 control_request")
	}
	// stdin 关了：插话放回队列，不算送达
	w.close()
	steers.push(protocol.Steer{ID: 2, Prompt: "x"})
	claudeControl(`{"type":"control_request","request_id":"r","request":{"subtype":"hook_callback","callback_id":"agentuse-pre"}}`, w, &inbox{steers: steers}, func(string, any) { n++ })
	if got := steers.take(); len(got) != 1 || n != 1 {
		t.Fatalf("closed: %+v %d", got, n)
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

const askLine = `{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"t1",` +
	`"input":{"questions":[{"question":"语言？","header":"语言","multiSelect":true,"options":[{"label":"Go"},{"label":"Python"}]}]}}}`

func TestClaudeAsk(t *testing.T) {
	var buf syncBuf
	w := &stdinWriter{w: nopCloser{&buf}}
	kinds := make(chan string, 10)
	var payload protocol.QuestionPayload
	report := func(kind string, p any) {
		if kind == "question" {
			payload = p.(protocol.QuestionPayload)
		}
		kinds <- kind
	}

	// 别的工具直接放行
	in := &inbox{asks: newAskBox(time.Minute)}
	claudeControl(`{"type":"control_request","request_id":"r0","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}}}`, w, in, report)
	if !strings.Contains(buf.String(), `"behavior":"allow","updatedInput":{"command":"ls"}`) {
		t.Fatalf("bash: %s", buf.String())
	}

	// 提问：先回报 question,不回 control_response;答案到了再回 allow,带上 answers
	buf.Reset()
	if !claudeControl(askLine, w, in, report) {
		t.Fatal("应该处理 control_request")
	}
	if k := <-kinds; k != "question" || payload.ToolUseID != "t1" || len(payload.Questions) != 1 || !payload.Questions[0].MultiSelect || payload.Questions[0].Options[1].Label != "Python" {
		t.Fatalf("question: %s %+v", k, payload)
	}
	if buf.String() != "" {
		t.Fatalf("还没答就回了: %s", buf.String())
	}
	in.asks.answer(protocol.Answer{ToolUseID: "other", Answers: map[string]string{"语言？": "Python"}})
	in.asks.answer(protocol.Answer{ToolUseID: "t1", Answers: map[string]string{"语言？": "Go"}})
	if k := <-kinds; k != "answered" || !strings.Contains(buf.String(), `"answers":{"语言？":"Go"}`) || !strings.Contains(buf.String(), `"request_id":"r1"`) || !strings.Contains(buf.String(), `"questions":[`) {
		t.Fatalf("answered: %s %s", k, buf.String())
	}

	// 超时：回 deny,回报 question_expired
	buf.Reset()
	in = &inbox{asks: newAskBox(20 * time.Millisecond)}
	claudeControl(askLine, w, in, report)
	<-kinds
	if k := <-kinds; k != "question_expired" || !strings.Contains(buf.String(), `"behavior":"deny"`) {
		t.Fatalf("timeout: %s %s", k, buf.String())
	}

	// 运行结束：还在等的作废
	in = &inbox{asks: newAskBox(time.Minute)}
	claudeControl(askLine, w, in, report)
	<-kinds
	in.asks.stop()
	if k := <-kinds; k != "question_expired" {
		t.Fatalf("stop: %s", k)
	}

	// 不能提问的 harness:直接 deny
	buf.Reset()
	claudeControl(askLine, w, &inbox{}, report)
	if !strings.Contains(buf.String(), `"behavior":"deny"`) || len(kinds) != 0 {
		t.Fatalf("no ask: %s", buf.String())
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
func (s *syncBuf) Reset()                      { s.mu.Lock(); defer s.mu.Unlock(); s.b.Reset() }
