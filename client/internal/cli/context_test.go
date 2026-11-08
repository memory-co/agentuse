package cli

import (
	"strings"
	"testing"
)

func TestContextHelpOnlyAdvertisesExecutionTools(t *testing.T) {
	t.Setenv("AGENTUSE_CONTEXT", "")
	t.Setenv("AGENTUSE_CONTEXT_RUN", "")
	var code int
	out, stderr := capture(t, func() { code = ContextMain([]string{"--help"}) })
	if code != 0 {
		t.Fatal(code, stderr)
	}
	help := out + stderr
	for _, expected := range []string{"context input", "context attachment list", "context attachment read FILE_ID", "context attachment upload FILE"} {
		if !strings.Contains(help, expected) {
			t.Fatal("missing execution tool", expected)
		}
	}
	for _, management := range []string{"register", "daemon", "user create", "--path", "--run", "--server", "--token"} {
		if strings.Contains(help, management) {
			t.Fatal("management option exposed", management)
		}
	}
	out, stderr = capture(t, func() { code = Main([]string{"context", "--help"}) })
	if code != 0 || !strings.Contains(out+stderr, "--path") || !strings.Contains(out+stderr, "enter") {
		t.Fatal(code, out, stderr)
	}
}
func TestContextRequiresBindingAndRejectsScopeOverride(t *testing.T) {
	t.Setenv("AGENTUSE_CONTEXT", "")
	var code int
	_, stderr := capture(t, func() { code = ContextMain([]string{"input"}) })
	if code != 1 || !strings.Contains(stderr, "未进入执行上下文") {
		t.Fatal(code, stderr)
	}
	for _, args := range [][]string{{"--path", "/other", "input"}, {"--run", "other", "input"}, {"--server", "https://other", "input"}, {"register"}, {"enter"}, {"work", "new"}, {"attachments"}, {"attachment", "at_old"}} {
		capture(t, func() { code = ContextMain(args) })
		if code != 2 {
			t.Fatal("bound command accepted a management operation", args, code)
		}
	}
	capture(t, func() { code = Main([]string{"attachment", "help"}) })
	if code != 2 {
		t.Fatal("top-level attachment command retained")
	}
	capture(t, func() { code = Main([]string{"work", "help"}) })
	if code != 2 {
		t.Fatal("work command retained by client")
	}
	capture(t, func() { code = Main([]string{"session", "help"}) })
	if code != 2 {
		t.Fatal("session alias retained")
	}
	for _, args := range [][]string{{"conversation", "read", "--session", "old"}, {"attachment", "list", "--session", "old"}} {
		capture(t, func() { code = Main(args) })
		if code != 2 {
			t.Fatal("old conversation flag retained", args, code)
		}
	}
	out, stderr := capture(t, func() { code = Main([]string{"conversation", "help"}) })
	if code != 0 || !strings.Contains(out, "agentuse conversation") {
		t.Fatal(code, out, stderr)
	}
}
