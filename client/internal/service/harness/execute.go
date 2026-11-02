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
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

type DriveResult struct {
	OK             bool            `json:"ok"`
	Output         string          `json:"output,omitempty"`
	Error          string          `json:"error,omitempty"`
	Usage          *agentuse.Usage `json:"usage,omitempty"`
	CostUSD        float64         `json:"cost_usd,omitempty"`
	RunIDs         []string        `json:"run_ids,omitempty"`
	CleanupPending bool            `json:"cleanup_pending,omitempty"`
}
type Reporter func(Event)
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type Question struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Header   string   `json:"header"`
	Multiple bool     `json:"multiple"`
	Options  []Option `json:"options"`
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
	accepted   func(Acceptance)
	input, run string
	done       bool
}
type pendingRequest struct {
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
func (s Service) DriveSession(ctx context.Context, a Adapter, prompt string, dir string, in *Input, report Reporter) (result DriveResult) {
	result.OK = true
	if report == nil {
		report = func(Event) {}
	}
	events := a.EventsPath
	if events == "" {
		return DriveResult{Error: "session events path is required"}
	}
	if err := os.MkdirAll(filepath.Dir(events), 0700); err != nil {
		return DriveResult{Error: err.Error()}
	}
	if strings.TrimSpace(prompt) == "" {
		return DriveResult{Error: "prompt must not be empty"}
	}
	var e error
	var offset int64
	if info, e := os.Stat(events); e == nil {
		offset = info.Size()
	} else if !os.IsNotExist(e) {
		return DriveResult{Error: e.Error()}
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
			report(event)
			if !seenRuns[event.RunID] {
				seenRuns[event.RunID] = true
				result.RunIDs = append(result.RunIDs, event.RunID)
			}
			if step.accepted != nil {
				step.accepted(Acceptance{RunID: event.RunID, InputID: event.InputID})
			}
			return
		}
	}
	fail := func(e error) DriveResult {
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
			if control.send != nil {
				step := &submission{prompt: control.send.Text, accepted: control.send.Accepted}
				submitted = append(submitted, step)
				if _, e = command(ctx, a, events, "send", step.prompt, "--cwd", dir); e != nil {
					return fail(e)
				}
			} else if control.reply != nil {
				_, err := command(ctx, a, events, "reply", control.reply.Body, control.reply.RequestID)
				if control.reply.Done != nil {
					control.reply.Done(err)
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
				if messageText[event.MessageID] == event.Text {
					continue
				}
				messageText[event.MessageID] = event.Text
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
					requests[event.RequestID] = pendingRequest{expires: time.Now().Add(timeout)}
				case "replied", "expired":
					delete(requests, event.RequestID)
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
			report(event)
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

// State is the public harnessctl status response, with no private process IDs.
type State struct {
	State    string            `json:"state"`
	RunID    *string           `json:"run_id"`
	Queued   int               `json:"queued"`
	Requests []json.RawMessage `json:"requests"`
	Cwd      string            `json:"cwd"`
	Model    *string           `json:"model"`
}

func (s Service) Status(ctx context.Context, a Adapter, events string) (State, error) {
	var state State
	data, err := command(ctx, a, events, "status", "")
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	return state, err
}
