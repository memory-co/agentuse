package service

import (
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/memory-co/agentuse/internal/protocol"
)

// steerQueue 是一条消息还没塞给 harness 的插话。nil 表示不收插话。
type steerQueue struct {
	mu    sync.Mutex
	items []protocol.Steer
	seen  map[int64]bool
}

func (q *steerQueue) push(s protocol.Steer) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if s.ID > 0 {
		if q.seen == nil {
			q.seen = map[int64]bool{}
		}
		if q.seen[s.ID] {
			return
		}
		q.seen[s.ID] = true
	}
	q.items = append(q.items, s)
}

// take 取走全部插话。
func (q *steerQueue) take() []protocol.Steer {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.items
	q.items = nil
	return out
}

// stdinWriter 串行写 harness 的 stdin,关掉以后再写直接丢掉。
type stdinWriter struct {
	mu     sync.Mutex
	w      io.WriteCloser
	closed bool
}

func (s *stdinWriter) write(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	_, err := s.w.Write(b)
	return err
}

func (s *stdinWriter) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.w.Close()
	}
}

func (s *stdinWriter) writeJSON(v any) error {
	b, _ := json.Marshal(v)
	return s.write(append(b, '\n'))
}

// claude-hooks:用 Claude Code stream-json 的 control 协议注册 hook,在 hook 回调里把插话塞进去。
// 见 docs/v2/designs/steer.md。
const (
	hookPre  = "agentuse-pre"
	hookStop = "agentuse-stop"
)

// claudeHooksInit 是 stdin 的第一行:注册 PreToolUse / Stop hook,只对这个进程有效。
func claudeHooksInit() map[string]any {
	return map[string]any{
		"type":       "control_request",
		"request_id": "agentuse-init",
		"request": map[string]any{
			"subtype": "initialize",
			"hooks": map[string]any{
				"PreToolUse": []any{map[string]any{"hookCallbackIds": []string{hookPre}}},
				"Stop":       []any{map[string]any{"hookCallbackIds": []string{hookStop}}},
			},
		},
	}
}

// steerText 是塞给 Claude 的插话说明。
func steerText(steers []protocol.Steer) string {
	prompts := make([]string, len(steers))
	for i, s := range steers {
		prompts[i] = s.Prompt
	}
	return "[用户插话] 用户在你工作时补充了下面的话。这是对原任务的补充，不是替换原任务：\n" + strings.Join(prompts, "\n\n")
}

// claudeControl 处理 stdout 的一行:是 control_request 就回 control_response 并返回 true。
// 每个 control_request 都必须回，不然 claude 会一直等。提问(can_use_tool + AskUserQuestion)在后台等答案再回。
func claudeControl(line string, w *stdinWriter, in *inbox, report reportFn) bool {
	var m struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype    string          `json:"subtype"`
			CallbackID string          `json:"callback_id"`
			ToolName   string          `json:"tool_name"`
			ToolUseID  string          `json:"tool_use_id"`
			Input      json.RawMessage `json:"input"`
		} `json:"request"`
	}
	if json.Unmarshal([]byte(line), &m) != nil || m.Type != "control_request" {
		return false
	}
	steers := in.steerQueue()
	var out any = map[string]any{}
	var sent []protocol.Steer
	switch m.Request.Subtype {
	case "can_use_tool":
		// bypassPermissions 下只有要人回答的工具会来问;别的直接放行
		switch {
		case m.Request.ToolName != "AskUserQuestion":
			input := m.Request.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out = map[string]any{"behavior": "allow", "updatedInput": input}
		case in.askBox() != nil:
			in.askBox().ask(m.RequestID, m.Request.ToolUseID, m.Request.Input, w, report)
			return true
		default:
			out = deny("没有人能回答提问，请自己做出合理的判断，继续完成任务。")
		}
	case "hook_callback":
		switch m.Request.CallbackID {
		case hookPre:
			if sent = steers.take(); len(sent) > 0 {
				out = map[string]any{"hookSpecificOutput": map[string]any{
					"hookEventName": "PreToolUse", "additionalContext": steerText(sent)}}
			}
		case hookStop:
			if sent = steers.take(); len(sent) > 0 {
				out = map[string]any{"decision": "block", "reason": steerText(sent)}
			}
		}
	}
	if err := writeControl(w, m.RequestID, out); err != nil {
		// 没塞进去:放回去，这一轮结束时服务会把它们转成下一条消息
		if steers != nil {
			steers.mu.Lock()
			steers.items = append(sent, steers.items...)
			steers.mu.Unlock()
		}
		return true
	}
	for _, s := range sent {
		report("steered", protocol.SteeredPayload{SteerID: s.ID})
	}
	return true
}
