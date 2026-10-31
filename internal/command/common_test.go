package command

import (
	"bytes"
	"errors"
	"flag"
	"github.com/memory-co/agentuse/internal/version"
	"io"
	"os"
	"strings"
	"testing"
)

// capture 跑 fn,返回它写到 stdout 和 stderr 的内容。
func capture(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	done := make(chan [2]string)
	go func() {
		var o, e bytes.Buffer
		ch := make(chan struct{})
		go func() { io.Copy(&e, errR); close(ch) }()
		io.Copy(&o, outR)
		<-ch
		done <- [2]string{o.String(), e.String()}
	}()
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	fn()
	outW.Close()
	errW.Close()
	r := <-done
	return r[0], r[1]
}

func TestDispatch(t *testing.T) {
	var got []string
	cmds := []Command{
		{"ok", func(args []string) error { got = args; return nil }},
		{"fail", func([]string) error { return errors.New("boom") }},
		{"usage", func([]string) error { return ErrUsage }},
		{"help", func([]string) error { return flag.ErrHelp }},
	}
	run := func(args ...string) (int, string, string) {
		var code int
		out, errOut := capture(t, func() { code = Dispatch("prog", "USAGE\n", args, cmds) })
		return code, out, errOut
	}
	if code, _, errOut := run(); code != 2 || errOut != "USAGE\n" {
		t.Fatalf("no args: %d %q", code, errOut)
	}
	for _, h := range []string{"help", "-h", "--help"} {
		if code, out, _ := run(h); code != 0 || out != "USAGE\n" {
			t.Fatalf("%s: %d %q", h, code, out)
		}
	}
	version.Version = "v1.2.3"
	defer func() { version.Version = "dev" }()
	for _, v := range []string{"version", "-v", "--version"} {
		if code, out, _ := run(v); code != 0 || out != "v1.2.3\n" {
			t.Fatalf("%s: %d %q", v, code, out)
		}
	}
	if code, _, errOut := run("nope"); code != 2 || !strings.Contains(errOut, "未知命令 prog nope") {
		t.Fatalf("unknown: %d %q", code, errOut)
	}
	if code, _, _ := run("ok", "a", "b"); code != 0 || strings.Join(got, ",") != "a,b" {
		t.Fatalf("ok: %d %v", code, got)
	}
	if code, _, errOut := run("fail"); code != 1 || errOut != "错误: boom\n" {
		t.Fatalf("fail: %d %q", code, errOut)
	}
	if code, _, _ := run("usage"); code != 2 {
		t.Fatalf("usage: %d", code)
	}
}

func TestParse(t *testing.T) {
	fs := NewFlags("x")
	n := fs.Int("n", 0, "")
	capture(t, func() {
		if err := Parse(fs, []string{"-n", "3"}); err != nil || *n != 3 {
			t.Fatalf("ok: %v", err)
		}
		if err := Parse(NewFlags("x"), []string{"-zzz"}); !errors.Is(err, ErrUsage) {
			t.Fatalf("unknown flag: %v", err)
		}
		if err := Parse(NewFlags("x"), []string{"-h"}); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("help: %v", err)
		}
	})
}

func TestPrintTable(t *testing.T) {
	var b bytes.Buffer
	PrintTable(&b, [][]string{{"名字", "命令", "版本"}, {"claude", "/usr/bin/claude", "2.0"}, {"中文名", "-", "未安装"}})
	want := "名字    命令             版本\n" +
		"claude  /usr/bin/claude  2.0\n" +
		"中文名  -                未安装\n"
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
	for s, w := range map[string]int{"abc": 3, "中文": 4, "ｆｕｌｌ": 8, "한국": 4, "": 0, "é": 1} {
		if displayWidth(s) != w {
			t.Errorf("displayWidth(%q) = %d, want %d", s, displayWidth(s), w)
		}
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("AGENTUSE_TEST_X", "")
	if EnvOr("AGENTUSE_TEST_X", "def") != "def" {
		t.Fatal("empty env")
	}
	t.Setenv("AGENTUSE_TEST_X", "v")
	if EnvOr("AGENTUSE_TEST_X", "def") != "v" {
		t.Fatal("env")
	}
}
