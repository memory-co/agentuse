package service

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
)

// defaultAskTimeout 是等用户回答提问的默认最长时间,daemon.json 的 ask_timeout 可改。
const defaultAskTimeout = 30 * time.Minute

// inbox 是一条正在跑的消息从服务那边收到的东西：插话和提问的答案。nil 表示都不收。
type inbox struct {
	steers *steerQueue
	asks   *askBox
}

func (in *inbox) steerQueue() *steerQueue {
	if in == nil {
		return nil
	}
	return in.steers
}

func (in *inbox) askBox() *askBox {
	if in == nil {
		return nil
	}
	return in.asks
}

// askBox 管 harness 正在等的提问,见 docs/v2/designs/question.md。
// 每个提问一个 goroutine 等答案、超时或这次运行结束，然后回 control_response。
type askBox struct {
	timeout time.Duration

	mu      sync.Mutex
	waiting map[string]chan map[string]string // tool_use_id → 答案
	done    chan struct{}
	stopped bool
	wg      sync.WaitGroup
}

func newAskBox(timeout time.Duration) *askBox {
	if timeout <= 0 {
		timeout = defaultAskTimeout
	}
	return &askBox{timeout: timeout, waiting: map[string]chan map[string]string{}, done: make(chan struct{})}
}

// answer 把服务推来的答案交给正在等的提问;已经不在等就忽略。
func (a *askBox) answer(ans protocol.Answer) {
	if a == nil {
		return
	}
	a.mu.Lock()
	ch := a.waiting[ans.ToolUseID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- ans.Answers:
		default:
		}
	}
}

// ask 回报 question,在后台等答案。input 是 AskUserQuestion 的原始参数。
func (a *askBox) ask(requestID, toolUseID string, input json.RawMessage, w *stdinWriter, report reportFn) {
	ch := make(chan map[string]string, 1)
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		writeControl(w, requestID, deny("这次运行已经结束"))
		return
	}
	a.waiting[toolUseID] = ch
	a.wg.Add(1)
	a.mu.Unlock()
	report("question", protocol.QuestionPayload{ToolUseID: toolUseID, Questions: askQuestions(input)})

	go func() {
		defer a.wg.Done()
		defer func() {
			a.mu.Lock()
			delete(a.waiting, toolUseID)
			a.mu.Unlock()
		}()
		t := time.NewTimer(a.timeout)
		defer t.Stop()
		select {
		case answers := <-ch:
			var in map[string]any
			_ = json.Unmarshal(input, &in)
			if in == nil {
				in = map[string]any{}
			}
			in["answers"] = answers
			if writeControl(w, requestID, map[string]any{"behavior": "allow", "updatedInput": in}) == nil {
				report("answered", protocol.QuestionPayload{ToolUseID: toolUseID})
				return
			}
		case <-t.C:
			writeControl(w, requestID, deny(fmt.Sprintf("用户在 %s 内没有回答。先结束这一轮，在最终回复里写清楚你的问题和各个选项。", humanDuration(a.timeout))))
		case <-a.done:
		}
		report("question_expired", protocol.QuestionPayload{ToolUseID: toolUseID})
	}()
}

// stop 在这次运行结束时调用：还在等的提问全部作废，等它们回报完再返回。
func (a *askBox) stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if !a.stopped {
		a.stopped = true
		close(a.done)
	}
	a.mu.Unlock()
	a.wg.Wait()
}

// askQuestions 把 Claude 的 AskUserQuestion 参数转成协议里的问题。
func askQuestions(input json.RawMessage) []protocol.Question {
	var in struct {
		Questions []struct {
			Question    string            `json:"question"`
			Header      string            `json:"header"`
			MultiSelect bool              `json:"multiSelect"`
			Options     []protocol.Option `json:"options"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(input, &in)
	out := make([]protocol.Question, 0, len(in.Questions))
	for _, q := range in.Questions {
		out = append(out, protocol.Question{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect, Options: q.Options})
	}
	return out
}

func deny(msg string) map[string]any { return map[string]any{"behavior": "deny", "message": msg} }

func writeControl(w *stdinWriter, requestID string, response any) error {
	return w.writeJSON(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": requestID, "response": response}})
}

func humanDuration(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d 分钟", int(d/time.Minute))
	}
	return d.String()
}
