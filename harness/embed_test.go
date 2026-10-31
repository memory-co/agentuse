package harness

import (
	"strings"
	"testing"
	"testing/fstest"
)

const okYAML = `
name: %s
command: x
run:
  new: [a]
  resume: [b]
output:
  format: jsonl
`

func TestLoadValidate(t *testing.T) {
	for _, c := range []struct {
		dir, yaml, err string
	}{
		{"x", strings.Replace(okYAML, "%s", "x", 1), ""},
		{"Bad", strings.Replace(okYAML, "%s", "Bad", 1), "只能用小写字母"},
		{"x", strings.Replace(okYAML, "%s", "y", 1), "和目录名"},
		{"x", strings.Replace(strings.Replace(okYAML, "%s", "x", 1), "command: x", "", 1), "缺 command"},
		{"x", strings.Replace(strings.Replace(okYAML, "%s", "x", 1), "resume: [b]", "", 1), "缺 run.new / run.resume"},
		{"x", strings.Replace(strings.Replace(okYAML, "%s", "x", 1), "jsonl", "xml", 1), "不支持的 output.format"},
		{"x", "name: [", "harness/x/harness.yaml"},
		{"x", strings.Replace(okYAML, "%s", "x", 1) + "input: {format: xml}\n", "不支持的 input.format"},
		{"x", strings.Replace(okYAML, "%s", "x", 1) + "input: {format: stream-json}\n", "缺 input.message"},
		{"x", strings.Replace(okYAML, "%s", "x", 1) + "workdir: {nope: .claude/skills}\n", "不是本目录下的子目录"},
		{"x", strings.Replace(okYAML, "%s", "x", 1) + "workdir: {skills: ../up}\n", "相对路径"},
		{"x", strings.Replace(okYAML, "%s", "x", 1) + "workdir: {skills: .claude/skills}\n", ""},
	} {
		specs, err := load(fstest.MapFS{c.dir + "/harness.yaml": {Data: []byte(c.yaml)}, c.dir + "/skills/s/SKILL.md": {Data: []byte("x")}})
		if c.err == "" {
			if err != nil || len(specs) != 1 {
				t.Fatalf("%s: %v %+v", c.dir, err, specs)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Fatalf("want %q, got %v", c.err, err)
		}
	}
}

func TestLoadSorted(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, n := range []string{"zeta", "alpha", "mid"} {
		fsys[n+"/harness.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(okYAML, "%s", n, 1))}
	}
	fsys["mid/hooks/readme.md"] = &fstest.MapFile{Data: []byte("x")} // 其它文件不影响
	specs, err := load(fsys)
	if err != nil || len(specs) != 3 || specs[0].Name != "alpha" || specs[2].Name != "zeta" {
		t.Fatalf("%v %+v", err, specs)
	}
}

// 内置的每个 harness.yaml 都能读、字段齐全。
func TestBuiltins(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.DisplayName == "" || s.Description == "" || len(s.Detect.Version) == 0 || (s.Transport == "" && len(s.Output.Rules) == 0) {
			t.Errorf("%s: 字段不全 %+v", s.Name, s)
		}
		if s.Transport == "" && !s.Finish.RequireResult && !s.Finish.RequireOK {
			t.Errorf("%s: 内置 harness 要写 finish", s.Name)
		}
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("Get unknown")
	}
}
