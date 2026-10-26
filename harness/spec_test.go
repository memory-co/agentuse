package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range all {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"claude", "codex"}) {
		t.Fatalf("names: %v", names)
	}
}

func TestArgs(t *testing.T) {
	sp, _ := Get("codex")
	got, _ := sp.Args("hi", "th-1", []string{"-s", "x"})
	if strings.Join(got, " ") != "exec --json --skip-git-repo-check -s x resume th-1 hi" {
		t.Fatalf("codex resume: %q", got)
	}
	sp, _ = Get("claude")
	got, _ = sp.Args("hi", "", nil)
	if strings.Join(got, " ") != "-p --input-format stream-json --output-format stream-json --verbose --permission-mode bypassPermissions --disallowedTools AskUserQuestion" {
		t.Fatalf("claude new: %q", got)
	}
	if _, err := sp.Args("", "", nil); err == nil {
		t.Fatal("空 prompt 应报错")
	}
}

func TestStdin(t *testing.T) {
	sp, _ := Get("claude")
	got := string(sp.Stdin(`说 "hi" {prompt}`))
	if got != `{"message":{"content":"说 \"hi\" {prompt}","role":"user"},"type":"user"}`+"\n" {
		t.Fatalf("claude stdin: %q", got)
	}
	if sp, _ := Get("codex"); sp.Stdin("hi") != nil {
		t.Fatal("codex 不写 stdin")
	}
}

func TestPrepare(t *testing.T) {
	sp, _ := Get("claude")
	dir := t.TempDir()
	stale := filepath.Join(dir, ".claude", "skills", "agentuse", "old.md")
	mine := filepath.Join(dir, ".claude", "skills", "mine", "SKILL.md")
	for _, p := range []string{stale, mine} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	if err := sp.Prepare(dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".claude", "skills", "agentuse", "SKILL.md")); err != nil || !strings.Contains(string(b), "name: agentuse") {
		t.Fatalf("skill: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("自带 skill 应该整个换成当前版本")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("会话目录里别的东西不该动")
	}
	if sp, _ := Get("codex"); sp.Prepare(t.TempDir()) != nil {
		t.Fatal("codex 没有 workdir")
	}
}

func run(t *testing.T, name string, lines ...string) (State, []string) {
	t.Helper()
	sp, ok := Get(name)
	if !ok {
		t.Fatal(name)
	}
	var st State
	var progress []string
	for _, l := range lines {
		sp.Line(&st, l, func(s string) { progress = append(progress, s) })
	}
	return st, progress
}

func TestClaudeOutput(t *testing.T) {
	st, progress := run(t, "claude",
		`{"type":"system","subtype":"init","session_id":"s-1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"看一下"},{"type":"text","text":"  "},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
		`not json`,
		`{"type":"result","subtype":"success","is_error":false,"result":"好了","session_id":"s-1","total_cost_usd":0.5}`,
	)
	want := []string{"看一下", `🔧 Bash {"command":"ls"}`, "not json"}
	if !reflect.DeepEqual(progress, want) {
		t.Fatalf("progress: %q", progress)
	}
	if st.SessionID != "s-1" || !st.SawResult || !st.ResultOK || st.Output != "好了" || st.CostUSD != 0.5 || st.Error != "" {
		t.Fatalf("state: %+v", st)
	}

	st, _ = run(t, "claude", `{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"s-2"}`)
	if st.ResultOK || st.Error != "error_max_turns" || st.SessionID != "s-2" {
		t.Fatalf("failed result: %+v", st)
	}
}

func TestCodexOutput(t *testing.T) {
	st, progress := run(t, "codex",
		`{"type":"thread.started","thread_id":"th-1"}`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"ls"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"done!"}}`,
		`{"type":"turn.completed"}`,
	)
	if !reflect.DeepEqual(progress, []string{"🔧 ls", "done!"}) || st.SessionID != "th-1" || st.Output != "done!" || !st.SawOK {
		t.Fatalf("%+v %q", st, progress)
	}
	st, _ = run(t, "codex", `{"type":"turn.failed","error":{"message":"boom"}}`)
	if st.SawOK || st.Error != "boom" {
		t.Fatalf("failed: %+v", st)
	}
}

func TestArgsPlaceholders(t *testing.T) {
	var sp Spec
	sp.Run.New = []string{"{args}", "--x", "{prompt}"}
	sp.Run.Resume = []string{"{resume}", "{prompt}", "{args}"}
	// {args} 没配就去掉;prompt 里的空格、引号不会拆开
	if got, _ := sp.Args(`a "b" c`, "", nil); !reflect.DeepEqual(got, []string{"--x", `a "b" c`}) {
		t.Fatalf("new: %q", got)
	}
	if got, _ := sp.Args("p", "r", []string{"-a", "-b"}); !reflect.DeepEqual(got, []string{"r", "p", "-a", "-b"}) {
		t.Fatalf("resume: %q", got)
	}
}

func TestHelpers(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": "x", "n": 1.5, "t": true, "o": map[string]any{"k": "v"}}, "s": "str"}
	for _, c := range []struct {
		path string
		want string
	}{{"a.b", "x"}, {"a.n", "1.5"}, {"a.t", "true"}, {"a.o", `{"k":"v"}`}, {"s", "str"}, {"s.x", ""}, {"nope", ""}, {"a.nope.deeper", ""}} {
		if got := str(lookup(m, c.path)); got != c.want {
			t.Errorf("str(lookup(%q)) = %q, want %q", c.path, got, c.want)
		}
	}
	if !matches(map[string]any{"a.b": "x", "a.t": true, "a.n": 1.5}, m) {
		t.Error("matches")
	}
	if matches(map[string]any{"a.b": "y"}, m) || matches(map[string]any{"missing": nil}, m) {
		t.Error("should not match")
	}
	if !matches(nil, m) {
		t.Error("empty match matches everything")
	}
	if got := render("{a.b}-{nope}-{s}", m); got != "x--str" {
		t.Errorf("render: %q", got)
	}
	if truncate("abcdef", 3) != "abc…" || truncate("ab", 3) != "ab" {
		t.Error("truncate")
	}
}

func TestLineEdgeCases(t *testing.T) {
	sp := Spec{Output: Output{Rules: []Rule{
		{Match: map[string]any{"type": "list"}, Each: "items", Rules: []Rule{{Progress: "{v}"}}},
		{Match: map[string]any{"type": "big"}, Progress: "{v}"},
		{Match: map[string]any{"type": "err"}, Error: "missing"},
	}}}
	var st State
	var progress []string
	add := func(s string) { progress = append(progress, s) }
	sp.Line(&st, "   ", add)                                                // 空行跳过
	sp.Line(&st, `{"type":"list","items":"not an array"}`, add)             // each 不是数组
	sp.Line(&st, `{"type":"list","items":[1,{"v":"a"},{"v":"  "}]}`, add)   // 非对象跳过，空白进度不报
	sp.Line(&st, `{"type":"big","v":"`+strings.Repeat("x", 5000)+`"}`, add) // 超长截断
	sp.Line(&st, `{"type":"err"}`, add)                                     // error 字段不存在不覆盖
	if len(progress) != 2 || progress[0] != "a" || len(progress[1]) != maxProgress+len("…") {
		t.Fatalf("progress: %d %q", len(progress), progress[0])
	}
	if st.Error != "" {
		t.Fatalf("state: %+v", st)
	}
}

func TestClaudePlan(t *testing.T) {
	tool := func(name, input string) string {
		return `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"` + name + `","input":` + input + `}]}}`
	}
	// TodoWrite:每次整份，不再出 🔧 进度
	st, progress := run(t, "claude",
		tool("TodoWrite", `{"todos":[{"content":"读代码","status":"completed","activeForm":"读代码中"},{"content":"改","status":"in_progress"}]}`))
	want := []Step{{ID: "1", Text: "读代码", Status: "completed"}, {ID: "2", Text: "改", Status: "in_progress"}}
	if !reflect.DeepEqual(st.Plan, want) || !st.PlanDirty || len(progress) != 0 {
		t.Fatalf("TodoWrite: %+v %v", st.Plan, progress)
	}

	// TaskCreate / TaskUpdate:按建的顺序编 id,deleted 删掉
	st, progress = run(t, "claude",
		tool("TaskCreate", `{"subject":"a","description":"…"}`),
		tool("TaskCreate", `{"subject":"b"}`),
		tool("TaskCreate", `{"subject":"c"}`),
		tool("TaskUpdate", `{"taskId":"1","status":"completed"}`),
		tool("TaskUpdate", `{"taskId":"2","status":"deleted"}`),
		tool("TaskUpdate", `{"taskId":"3","status":"in_progress"}`),
		tool("TaskUpdate", `{"taskId":"9","status":"completed"}`),
		tool("Bash", `{"command":"ls"}`))
	want = []Step{{ID: "1", Text: "a", Status: "completed"}, {ID: "3", Text: "c", Status: "in_progress"}}
	if !reflect.DeepEqual(st.Plan, want) || !reflect.DeepEqual(progress, []string{`🔧 Bash {"command":"ls"}`}) {
		t.Fatalf("Task*: %+v %v", st.Plan, progress)
	}
}
