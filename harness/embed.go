// Package harness 读仓库顶层 harness/ 目录：一个内置 harness 一个子目录，
// 里面的 harness.yaml 描述这个 CLI 的命令入口点位(探测、新建 / 续接会话、怎么读输出)。
// 见 docs/v2/designs/harness.md。
package harness

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed */harness.yaml
var files embed.FS

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var (
	loadOnce sync.Once
	specs    []Spec
	loadErr  error
)

// All 返回全部内置 harness,按名字排序。
func All() ([]Spec, error) {
	loadOnce.Do(func() { specs, loadErr = load(files) })
	return specs, loadErr
}

// Get 返回名字为 name 的内置 harness。
func Get(name string) (Spec, bool) {
	all, _ := All()
	for _, s := range all {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

func load(fsys fs.FS) ([]Spec, error) {
	paths, err := fs.Glob(fsys, "*/harness.yaml")
	if err != nil {
		return nil, err
	}
	var out []Spec
	for _, p := range paths {
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		var s Spec
		if err := yaml.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("harness/%s: %w", p, err)
		}
		if err := s.validate(path.Dir(p)); err != nil {
			return nil, fmt.Errorf("harness/%s: %w", p, err)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s Spec) validate(dir string) error {
	switch {
	case !nameRe.MatchString(s.Name):
		return fmt.Errorf("name %q 只能用小写字母、数字和 -", s.Name)
	case s.Name != dir:
		return fmt.Errorf("name %q 和目录名 %q 不一致", s.Name, dir)
	case s.Command == "":
		return fmt.Errorf("缺 command")
	case len(s.Run.New) == 0 || len(s.Run.Resume) == 0:
		return fmt.Errorf("缺 run.new / run.resume")
	case s.Output.Format != "jsonl":
		return fmt.Errorf("不支持的 output.format %q", s.Output.Format)
	}
	return nil
}
