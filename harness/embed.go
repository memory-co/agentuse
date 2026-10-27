// Package harness 读仓库顶层 harness/ 目录：一个内置 harness 一个子目录，
// 里面的 harness.yaml 描述这个 CLI 的命令入口点位(探测、新建 / 续接会话、怎么读输出)。
// 见 docs/v2/designs/harness.md。
package harness

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed */harness.yaml */skills
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
		if err := s.validate(fsys, path.Dir(p)); err != nil {
			return nil, fmt.Errorf("harness/%s: %w", p, err)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s Spec) validate(fsys fs.FS, dir string) error {
	for src, dst := range s.Workdir {
		if st, err := fs.Stat(fsys, path.Join(dir, src)); err != nil || !st.IsDir() || strings.Contains(src, "/") {
			return fmt.Errorf("workdir: %q 不是本目录下的子目录", src)
		}
		if dst == "" || filepath.IsAbs(dst) || strings.HasPrefix(path.Clean(dst), "..") {
			return fmt.Errorf("workdir: %q 必须是会话目录里的相对路径", dst)
		}
	}
	switch {
	case s.Input.Format != "" && s.Input.Format != "stream-json":
		return fmt.Errorf("不支持的 input.format %q", s.Input.Format)
	case s.Input.Format != "" && len(s.Input.Message) == 0:
		return fmt.Errorf("缺 input.message")
	case s.Steer != "" && s.Steer != SteerClaudeHooks:
		return fmt.Errorf("不支持的 steer %q", s.Steer)
	case s.Steer == SteerClaudeHooks && s.Input.Format != "stream-json":
		return fmt.Errorf("steer: claude-hooks 要求 input.format: stream-json")
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

// Prepare 按 workdir 把本目录下的子目录拷进会话目录 dir。
// 每一项(如 skills/agentuse)先整个删掉再拷，保证是当前版本;会话目录里别的东西不动。
func (s Spec) Prepare(dir string) error {
	for src, dst := range s.Workdir {
		root := path.Join(s.Name, src)
		entries, err := fs.ReadDir(files, root)
		if err != nil {
			return err
		}
		for _, e := range entries {
			to := filepath.Join(dir, filepath.FromSlash(dst), e.Name())
			if err := os.RemoveAll(to); err != nil {
				return err
			}
			if err := copyTree(path.Join(root, e.Name()), to); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyTree(from, to string) error {
	return fs.WalkDir(files, from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(to, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(p, from), "/")))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
