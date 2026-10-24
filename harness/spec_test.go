package harness

import (
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
	if strings.Join(got, " ") != "-p hi --output-format stream-json --verbose" {
		t.Fatalf("claude new: %q", got)
	}
	if _, err := sp.Args("", "", nil); err == nil {
		t.Fatal("空 prompt 应报错")
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
