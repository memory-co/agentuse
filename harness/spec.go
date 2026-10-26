package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Spec 是一个 harness.yaml。
type Spec struct {
	Name        string `yaml:"name"`
	DisplayName string `yaml:"display_name"`
	Description string `yaml:"description"`
	// Command 是默认可执行文件,daemon.json 里配了就用配的。
	Command string `yaml:"command"`
	Detect  struct {
		Version []string `yaml:"version"`
	} `yaml:"detect"`
	Run struct {
		New    []string `yaml:"new"`
		Resume []string `yaml:"resume"`
	} `yaml:"run"`
	Input Input `yaml:"input"`
	// Workdir:本目录下的子目录 → 会话目录里的相对路径，起进程前拷过去。
	Workdir map[string]string `yaml:"workdir"`
	Output  Output            `yaml:"output"`
	Finish  Finish            `yaml:"finish"`
}

// Input 描述 stdin。Format 为空时 stdin 为空,prompt 只能放进参数。
type Input struct {
	Format  string         `yaml:"format"`  // "" 或 stream-json
	Message map[string]any `yaml:"message"` // stream-json 时写进 stdin 的那一行,字符串值里的 {prompt} 会被替换
}

type Output struct {
	Format    string `yaml:"format"`
	SessionID string `yaml:"session_id"`
	Rules     []Rule `yaml:"rules"`
}

// Rule 是一条逐行匹配的规则，字段含义见 docs/v2/designs/harness.md。
type Rule struct {
	Match     map[string]any `yaml:"match"`
	Each      string         `yaml:"each"`
	Rules     []Rule         `yaml:"rules"`
	Progress  string         `yaml:"progress"`
	SessionID string         `yaml:"session_id"`
	Output    string         `yaml:"output"`
	Error     string         `yaml:"error"`
	OK        bool           `yaml:"ok"`
	Result    *ResultRule    `yaml:"result"`
}

type ResultRule struct {
	OK      map[string]any `yaml:"ok"`
	Output  string         `yaml:"output"`
	Error   string         `yaml:"error"`
	CostUSD string         `yaml:"cost_usd"`
}

type Finish struct {
	RequireResult bool `yaml:"require_result"`
	RequireOK     bool `yaml:"require_ok"`
}

// Args 按 run.new / run.resume 拼出命令行参数(不含可执行文件本身)。
// 占位符必须独占一个参数:{prompt}、{resume} 换成一个参数,{args} 展开成 extra。
func (s Spec) Args(prompt, resume string, extra []string) ([]string, error) {
	if prompt == "" {
		return nil, errors.New("input.prompt 为空")
	}
	tmpl := s.Run.New
	if resume != "" {
		tmpl = s.Run.Resume
	}
	out := make([]string, 0, len(tmpl)+len(extra))
	for _, a := range tmpl {
		switch a {
		case "{prompt}":
			out = append(out, prompt)
		case "{resume}":
			out = append(out, resume)
		case "{args}":
			out = append(out, extra...)
		default:
			out = append(out, a)
		}
	}
	return out, nil
}

// Stdin 按 input 生成要写进 stdin 的内容(一行 JSON),不需要 stdin 时返回 nil。
func (s Spec) Stdin(prompt string) []byte {
	if s.Input.Format != "stream-json" {
		return nil
	}
	b, _ := json.Marshal(fill(s.Input.Message, prompt))
	return append(b, '\n')
}

// fill 把 v 里所有字符串值中的 {prompt} 换成 prompt。
func fill(v any, prompt string) any {
	switch v := v.(type) {
	case string:
		return strings.ReplaceAll(v, "{prompt}", prompt)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = fill(x, prompt)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = fill(x, prompt)
		}
		return out
	default:
		return v
	}
}

// State 是读输出过程中攒下的结果。
type State struct {
	SessionID string
	Output    string
	Error     string
	CostUSD   float64
	SawOK     bool // 见过 ok: true 的规则
	SawResult bool // 见过 result 规则
	ResultOK  bool // result 规则判定的成败
}

const maxProgress = 4000

// Line 处理 stdout 的一行:更新 st,有进度就调用 progress。不是 JSON 对象的行当文本进度。
func (s Spec) Line(st *State, line string, progress func(text string)) {
	if strings.TrimSpace(line) == "" {
		return
	}
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		progress(truncate(line, maxProgress))
		return
	}
	if s.Output.SessionID != "" {
		if v := str(lookup(m, s.Output.SessionID)); v != "" {
			st.SessionID = v
		}
	}
	applyRules(s.Output.Rules, m, st, progress)
}

func applyRules(rules []Rule, m map[string]any, st *State, progress func(string)) {
	for _, r := range rules {
		if !matches(r.Match, m) {
			continue
		}
		if r.Each != "" {
			items, _ := lookup(m, r.Each).([]any)
			for _, it := range items {
				if im, ok := it.(map[string]any); ok {
					applyRules(r.Rules, im, st, progress)
				}
			}
		}
		if r.Progress != "" {
			if t := strings.TrimSpace(render(r.Progress, m)); t != "" {
				progress(truncate(t, maxProgress))
			}
		}
		if r.SessionID != "" {
			if v := str(lookup(m, r.SessionID)); v != "" {
				st.SessionID = v
			}
		}
		if r.Output != "" {
			st.Output = str(lookup(m, r.Output))
		}
		if r.Error != "" {
			if v := str(lookup(m, r.Error)); v != "" {
				st.Error = v
			}
		}
		if r.OK {
			st.SawOK = true
		}
		if rr := r.Result; rr != nil {
			st.SawResult = true
			st.ResultOK = matches(rr.OK, m)
			if rr.Output != "" {
				st.Output = str(lookup(m, rr.Output))
			}
			if rr.CostUSD != "" {
				st.CostUSD, _ = lookup(m, rr.CostUSD).(float64)
			}
			if !st.ResultOK && rr.Error != "" {
				st.Error = str(lookup(m, rr.Error))
			}
		}
	}
}

// matches:cond 里每个字段路径的值都和期望值相等。字段不存在算不相等。
func matches(cond map[string]any, m map[string]any) bool {
	for p, want := range cond {
		got := lookup(m, p)
		if got == nil || fmt.Sprint(got) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

// lookup 按 a.b.c 取嵌套字段。
func lookup(m map[string]any, p string) any {
	var cur any = m
	for _, k := range strings.Split(p, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

// str:字符串原样，其他值按 JSON 原文，不存在为空。
func str(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

var placeholderRe = regexp.MustCompile(`\{([A-Za-z0-9_.]+)\}`)

func render(tmpl string, m map[string]any) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(s string) string {
		return str(lookup(m, s[1:len(s)-1]))
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
