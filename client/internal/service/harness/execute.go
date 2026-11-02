package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

type Result struct {
	OK             bool            `json:"ok"`
	Output         string          `json:"output,omitempty"`
	Error          string          `json:"error,omitempty"`
	Usage          *agentuse.Usage `json:"usage,omitempty"`
	CostUSD        float64         `json:"cost_usd,omitempty"`
	HarnessRunIDs  []string        `json:"harness_run_ids,omitempty"`
	CleanupPending bool            `json:"cleanup_pending,omitempty"`
}
type Reporter func(string, any)
type Question struct {
	ID       string            `json:"id"`
	Text     string            `json:"text"`
	Header   string            `json:"header"`
	Multiple bool              `json:"multiple"`
	Options  []protocol.Option `json:"options"`
}
type Event struct {
	Type      string                     `json:"type"`
	RunID     string                     `json:"run_id"`
	InputID   string                     `json:"input_id"`
	InputIDs  []string                   `json:"input_ids"`
	Text      string                     `json:"text"`
	MessageID string                     `json:"message_id"`
	RequestID string                     `json:"request_id"`
	State     string                     `json:"state"`
	Kind      string                     `json:"kind"`
	Message   string                     `json:"message"`
	Questions []Question                 `json:"questions"`
	Status    string                     `json:"status"`
	Output    string                     `json:"output"`
	Error     *string                    `json:"error"`
	Usage     *agentuse.Usage            `json:"usage"`
	CostUSD   *float64                   `json:"cost_usd"`
	Topic     string                     `json:"topic"`
	Data      map[string]json.RawMessage `json:"data"`
}
type submission struct {
	prompt     string
	steerID    int64
	input, run string
	done       bool
}
type pendingRequest struct {
	event   Event
	expires time.Time
}

func command(ctx context.Context, a Adapter, events, verb, body string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	argv := append([]string{verb, events}, args...)
	cmd := exec.CommandContext(ctx, a.Path, argv...)
	cmd.Env = append(os.Environ(), a.Env...)
	cmd.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		return nil, fmt.Errorf("harnessctl %s: %w: %s", verb, e, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Cancel uses only the public protocol and confirms the adapter has stopped foreground work.
func Cancel(a Adapter, events string) error {
	if _, e := os.Stat(events); os.IsNotExist(e) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, e := command(ctx, a, events, "cancel", ""); e != nil {
		return e
	}
	for ctx.Err() == nil {
		data, e := command(ctx, a, events, "status", "")
		if e != nil {
			return e
		}
		var state struct {
			State string `json:"state"`
		}
		if e = json.Unmarshal(data, &state); e != nil {
			return e
		}
		if state.State == "idle" || state.State == "failed" {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("harnessctl cancellation not confirmed: %w", ctx.Err())
}
func EventsPath(workspace, sessionID string) string {
	return filepath.Join(filepath.Dir(workspace), "sessions", sessionID, "events.jsonl")
}
func promptFor(m protocol.Dispatch, history string) (string, error) {
	var pi protocol.PromptInput
	if json.Unmarshal(m.Input, &pi) != nil || strings.TrimSpace(pi.Prompt) == "" {
		if m.Local && json.Valid(m.Input) {
			return string(m.Input), nil
		}
		return "", fmt.Errorf("input.prompt must not be empty")
	}
	instructions := instructionsFor(m)
	if history == "" {
		instructions = strings.ReplaceAll(instructions, "先读取 .agentuse/session-history.json。", "本次没有新增共享历史。")
	}
	if history != "" {
		instructions = strings.ReplaceAll(instructions, ".agentuse/session-history.json", history)
	}
	if instructions != "" {
		return instructions + "\n\n# 本次任务\n" + pi.Prompt, nil
	}
	return pi.Prompt, nil
}
func readEvents(path string, offset *int64) ([]Event, error) {
	file, e := os.Open(path)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer file.Close()
	if _, e = file.Seek(*offset, io.SeekStart); e != nil {
		return nil, e
	}
	reader := bufio.NewReader(io.LimitReader(file, (16<<20)+1))
	out := []Event{}
	for {
		line, e := reader.ReadBytes('\n')
		if len(line) > 16<<20 {
			return nil, fmt.Errorf("harnessctl event exceeds 16 MiB")
		}
		if e == io.EOF {
			return out, nil
		}
		if e != nil {
			return nil, e
		}
		var event Event
		if e = json.Unmarshal(line, &event); e != nil {
			return nil, fmt.Errorf("invalid harnessctl event: %w", e)
		}
		switch event.Type {
		case "accepted", "message", "request", "result":
			if _, e = uuid.Parse(event.RunID); e != nil {
				return nil, fmt.Errorf("invalid harnessctl run_id")
			}
		case "log":
		default:
			return nil, fmt.Errorf("unsupported harnessctl event %q", event.Type)
		}
		*offset += int64(len(line))
		out = append(out, event)
	}
}
func (s Service) Execute(ctx context.Context, a Adapter, m protocol.Dispatch, dir string, in *Input, report Reporter) (result Result) {
	result.OK = true
	if report == nil {
		report = func(string, any) {}
	}
	events := a.EventsPath
	if events == "" {
		events = filepath.Join(filepath.Dir(dir), "harnessctl", a.Name+"-"+m.AgentID, "events.jsonl")
	}
	if e := os.MkdirAll(filepath.Dir(events), 0700); e != nil {
		return Result{Error: e.Error()}
	}
	prompt, e := promptFor(m, a.HistoryPath)
	if e != nil {
		return Result{Error: e.Error()}
	}
	var offset int64
	if info, e := os.Stat(events); e == nil {
		offset = info.Size()
	} else if !os.IsNotExist(e) {
		return Result{Error: e.Error()}
	}
	submitted := []*submission{{prompt: prompt}}
	requests := map[string]pendingRequest{}
	seenRuns := map[string]bool{}
	seenResults := map[string]bool{}
	messageText := map[string]string{}
	defer func() {
		if in != nil {
			in.mu.Lock()
			in.closed = true
			in.mu.Unlock()
		}
	}()
	bindAccepted := func(event Event) {
		for _, step := range submitted {
			if step.input != "" || step.prompt != event.Text {
				continue
			}
			step.input = event.InputID
			step.run = event.RunID
			report("accepted", map[string]string{"harness_run_id": event.RunID})
			if !seenRuns[event.RunID] {
				seenRuns[event.RunID] = true
				result.HarnessRunIDs = append(result.HarnessRunIDs, event.RunID)
			}
			if step.steerID > 0 {
				report("steered", protocol.SteeredPayload{SteerID: step.steerID})
			}
			return
		}
	}
	fail := func(e error) Result {
		result.OK = false
		result.Error = e.Error()
		if cleanup := Cancel(a, events); cleanup != nil {
			result.CleanupPending = true
			result.Error += "; " + cleanup.Error()
		}
		// A timed-out command may still have durably accepted its input.
		// Keep that adapter-owned association even on cancellation/failure.
		if batch, readErr := readEvents(events, &offset); readErr == nil {
			for _, event := range batch {
				if event.Type == "accepted" {
					bindAccepted(event)
				}
			}
		}
		return result
	}
	if _, e = command(ctx, a, events, "send", prompt, "--cwd", dir); e != nil {
		return fail(e)
	}
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	lastPoll := time.Now()
	idlePolls := 0
	for {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		for _, control := range in.take() {
			if control.steer != nil {
				step := &submission{prompt: control.steer.Prompt, steerID: control.steer.ID}
				submitted = append(submitted, step)
				if _, e = command(ctx, a, events, "send", step.prompt, "--cwd", dir); e != nil {
					return fail(e)
				}
			} else if control.answer != nil {
				pending, ok := requests[control.answer.ToolUseID]
				if !ok {
					continue
				}
				event := pending.event
				answer := ""
				if event.Kind == "permission" {
					answer = control.answer.Answers[event.Message]
					if answer == "" {
						for _, value := range control.answer.Answers {
							answer = value
							break
						}
					}
				} else {
					answers := map[string]string{}
					for _, q := range event.Questions {
						v := control.answer.Answers[q.Text]
						if v == "" {
							v = control.answer.Answers[q.ID]
						}
						answers[q.ID] = v
					}
					data, _ := json.Marshal(answers)
					answer = string(data)
				}
				if _, e = command(ctx, a, events, "reply", answer, event.RequestID); e != nil {
					return fail(e)
				}
			}
		}
		batch, e := readEvents(events, &offset)
		if e != nil {
			return fail(e)
		}
		for _, event := range batch {
			if event.Type == "accepted" {
				bindAccepted(event)
				continue
			}
			if !seenRuns[event.RunID] {
				continue
			}
			switch event.Type {
			case "message":
				if messageText[event.MessageID] != event.Text {
					messageText[event.MessageID] = event.Text
					report("progress", map[string]string{"text": event.Text, "harness_run_id": event.RunID})
				}
			case "log":
				if event.Topic == "tool.started" {
					var title string
					_ = json.Unmarshal(event.Data["title"], &title)
					if title != "" {
						report("progress", map[string]string{"text": "🔧 " + title})
					}
				}
				if event.Topic == "plan" {
					report("plan", map[string]any{"steps": json.RawMessage(event.Data["steps"])})
				}
			case "request":
				switch event.State {
				case "pending":
					if _, ok := requests[event.RequestID]; ok {
						continue
					}
					timeout := DefaultAskTimeout
					if in != nil {
						timeout = in.timeout
					}
					requests[event.RequestID] = pendingRequest{event: event, expires: time.Now().Add(timeout)}
					questions := []protocol.Question{}
					if event.Kind == "permission" {
						questions = append(questions, protocol.Question{Question: event.Message, Options: []protocol.Option{{Label: "allow", Description: "允许一次"}, {Label: "deny", Description: "拒绝"}}})
					} else {
						for _, q := range event.Questions {
							questions = append(questions, protocol.Question{Question: q.Text, Header: q.Header, MultiSelect: q.Multiple, Options: q.Options})
						}
					}
					report("question", protocol.QuestionPayload{ToolUseID: event.RequestID, Questions: questions})
				case "replied", "expired":
					delete(requests, event.RequestID)
					kind := "answered"
					if event.State == "expired" {
						kind = "question_expired"
					}
					report(kind, protocol.QuestionPayload{ToolUseID: event.RequestID})
				default:
					return fail(fmt.Errorf("invalid harnessctl request state %q", event.State))
				}
			case "result":
				if seenResults[event.RunID] {
					continue
				}
				seenResults[event.RunID] = true
				for _, step := range submitted {
					for _, id := range event.InputIDs {
						if step.input == id && step.run == event.RunID {
							step.done = true
						}
					}
				}
				if event.Status != "completed" {
					result.OK = false
					if event.Error != nil {
						result.Error = *event.Error
					} else {
						result.Error = event.Status
					}
				}
				if result.Output != "" && event.Output != "" {
					result.Output += "\n\n"
				}
				result.Output += event.Output
				if event.Usage != nil {
					if result.Usage == nil {
						result.Usage = &agentuse.Usage{}
					}
					result.Usage.InputTokens += event.Usage.InputTokens
					result.Usage.OutputTokens += event.Usage.OutputTokens
					result.Usage.CacheReadTokens += event.Usage.CacheReadTokens
					result.Usage.CacheWriteTokens += event.Usage.CacheWriteTokens
				}
				if event.CostUSD != nil {
					result.CostUSD += *event.CostUSD
				}
			}
		}
		for _, pending := range requests {
			if time.Now().After(pending.expires) {
				return fail(fmt.Errorf("harnessctl request timed out"))
			}
		}
		done := true
		for _, step := range submitted {
			done = done && step.done
		}
		if done && in.finish() {
			return result
		}
		// Detect a dead backend through the public status command, never private PID/native data.
		if len(batch) == 0 {
			if time.Since(lastPoll) > time.Second {
				lastPoll = time.Now()
				data, statusErr := command(ctx, a, events, "status", "")
				if statusErr != nil {
					return fail(statusErr)
				}
				var state struct {
					State string `json:"state"`
				}
				if e = json.Unmarshal(data, &state); e != nil {
					return fail(e)
				}
				if state.State == "idle" || state.State == "failed" {
					idlePolls++
				} else {
					idlePolls = 0
				}
				if idlePolls > 1 {
					return fail(fmt.Errorf("harnessctl stopped without results for accepted inputs"))
				}
			}
			select {
			case <-ctx.Done():
				return fail(ctx.Err())
			case <-tick.C:
			}
		}
	}
}
