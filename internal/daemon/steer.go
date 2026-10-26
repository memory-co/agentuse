package daemon

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
}

func (q *steerQueue) push(s protocol.Steer) {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.items = append(q.items, s)
	q.mu.Unlock()
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
// 每个 control_request 都必须回，不然 claude 会一直等。
func claudeControl(line string, w *stdinWriter, steers *steerQueue, report reportFn) bool {
	var m struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype    string `json:"subtype"`
			CallbackID string `json:"callback_id"`
		} `json:"request"`
	}
	if json.Unmarshal([]byte(line), &m) != nil || m.Type != "control_request" {
		return false
	}
	out := map[string]any{}
	var sent []protocol.Steer
	if m.Request.Subtype == "hook_callback" {
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
	err := w.writeJSON(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": m.RequestID, "response": out}})
	if err != nil {
		// 没塞进去:放回去，这一轮结束时服务会把它们转成下一条消息
		for _, s := range sent {
			steers.push(s)
		}
		return true
	}
	for _, s := range sent {
		report("steered", protocol.SteeredPayload{SteerID: s.ID})
	}
	return true
}
