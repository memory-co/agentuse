package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/memory-co/agentuse/internal/version"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
)

// codexArgs translates daemon.json's Codex options to app-server config overrides.
// Defaults match our noninteractive exec environment; explicit options follow them.
func codexArgs(extra []string) ([]string, error) {
	var prefix []string
	args := []string{"app-server", "--listen", "stdio://", "-c", "sandbox_mode=\"workspace-write\"", "-c", "sandbox_workspace_write.network_access=true", "-c", "approval_policy=\"never\""}
	for i := 0; i < len(extra); i++ {
		flag, value, inline := strings.Cut(extra[i], "=")
		key := ""
		switch flag {
		case "-m", "--model":
			key = "model"
		case "-s", "--sandbox":
			key = "sandbox_mode"
		case "-a", "--ask-for-approval":
			key = "approval_policy"
		case "-p", "--profile":
		case "-c", "--config", "--enable", "--disable":
		case "--search":
			args = append(args, "-c", "web_search=\"live\"")
			continue
		case "--full-auto":
			args = append(args, "-c", "approval_policy=\"on-request\"", "-c", "sandbox_mode=\"workspace-write\"")
			continue
		case "--yolo", "--dangerously-bypass-approvals-and-sandbox":
			args = append(args, "-c", "approval_policy=\"never\"", "-c", "sandbox_mode=\"danger-full-access\"")
			continue
		default:
			return nil, fmt.Errorf("Codex app-server 不支持参数 %q，请使用 -c key=value 配置", extra[i])
		}
		if !inline {
			i++
			if i >= len(extra) {
				return nil, fmt.Errorf("Codex 参数 %s 缺少值", flag)
			}
			value = extra[i]
		}
		if flag == "-p" || flag == "--profile" {
			prefix = append(prefix, "--profile", value)
			continue
		}
		if key != "" {
			args = append(args, "-c", key+"="+strconv.Quote(value))
		} else {
			args = append(args, flag, value)
		}
	}
	return append(prefix, args...), nil
}

type codexFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
type codexRequest struct {
	method string
	at     time.Time
	steers []protocol.Steer
}

func codexText(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}}
}

// runCodex owns one fresh thread/turn. Only a matching turn/steer response can
// acknowledge delivery; a write to stdin alone is never treated as acceptance.
func runCodex(ctx context.Context, la localHarness, m protocol.Dispatch, dir string, in *inbox, report reportFn) Result {
	args, err := codexArgs(la.Args)
	if err != nil {
		return Result{Error: err.Error()}
	}
	var input protocol.PromptInput
	if err = json.Unmarshal(m.Input, &input); err != nil || strings.TrimSpace(input.Prompt) == "" {
		return Result{Error: "Codex input.prompt 不能为空"}
	}
	if ctx.Err() != nil {
		return Result{Error: ctx.Err().Error()}
	}
	procCtx, stop := context.WithCancel(context.Background())
	defer stop()
	cmd := exec.CommandContext(procCtx, la.Path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), la.Env...)
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return Result{Error: err.Error()}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		pipe.Close()
		return Result{Error: err.Error()}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		pipe.Close()
		return Result{Error: err.Error()}
	}
	if err = cmd.Start(); err != nil {
		pipe.Close()
		return Result{Error: err.Error()}
	}
	frames := make(chan codexFrame, 64)
	readErr := make(chan error, 1)
	var wg sync.WaitGroup
	var errTail tail
	var errMu sync.Mutex
	stderrText := func() string { errMu.Lock(); defer errMu.Unlock(); return truncate(errTail.String(), 2000) }
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanLines(stderr, func(line string) { errMu.Lock(); errTail.add(line); errMu.Unlock() })
	}()
	go func() {
		defer wg.Done()
		defer close(frames)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 16<<20)
		for scanner.Scan() {
			var frame codexFrame
			if e := json.Unmarshal(scanner.Bytes(), &frame); e != nil {
				readErr <- fmt.Errorf("Codex app-server 非法 JSON: %w", e)
				return
			}
			select {
			case frames <- frame:
			case <-procCtx.Done():
				return
			}
		}
		if e := scanner.Err(); e != nil {
			readErr <- e
		}
	}()
	defer func() { stop(); pipe.Close(); wg.Wait(); _ = cmd.Wait() }()
	writer := &stdinWriter{w: pipe}
	pending := map[int64]codexRequest{}
	var seq int64
	request := func(method string, params any, steers []protocol.Steer) error {
		seq++
		pending[seq] = codexRequest{method: method, at: time.Now(), steers: steers}
		return writer.writeJSON(map[string]any{"id": seq, "method": method, "params": params})
	}
	if err = request("initialize", map[string]any{"clientInfo": map[string]string{"name": "agentuse", "title": "agentuse", "version": version.Version}}, nil); err != nil {
		return Result{Error: err.Error()}
	}
	var thread, turn string
	var result Result
	var completed bool
	var steering bool
	var steerRejected bool
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if completed && !steering {
			return result
		}
		select {
		case <-ctx.Done():
			if thread != "" && turn != "" && !completed {
				_ = writer.writeJSON(map[string]any{"id": seq + 1, "method": "turn/interrupt", "params": map[string]string{"threadId": thread, "turnId": turn}})
				timer := time.NewTimer(2 * time.Second)
			shutdown:
				for {
					select {
					case frame, ok := <-frames:
						if !ok {
							break shutdown
						}
						if frame.Method == "turn/completed" {
							break shutdown
						}
					case <-timer.C:
						break shutdown
					}
				}
				timer.Stop()
			}
			return Result{Error: ctx.Err().Error(), SessionID: thread}
		case <-tick.C:
			for _, p := range pending {
				if time.Since(p.at) > 30*time.Second {
					return Result{Error: "Codex app-server " + p.method + " 响应超时", SessionID: thread}
				}
			}
			if turn != "" && thread != "" && !completed && !steering && !steerRejected {
				steers := in.steerQueue().take()
				if len(steers) > 0 {
					steering = true
					if err = request("turn/steer", map[string]any{"threadId": thread, "expectedTurnId": turn, "input": codexText(steerText(steers))}, steers); err != nil {
						return Result{Error: err.Error(), SessionID: thread}
					}
				}
			}
		case frame, ok := <-frames:
			if !ok {
				select {
				case e := <-readErr:
					return Result{Error: e.Error(), SessionID: thread}
				default:
				}
				return Result{Error: strings.TrimSpace("Codex app-server 在 turn 完成前退出 " + stderrText()), SessionID: thread}
			}
			if frame.Method != "" && len(frame.ID) > 0 {
				// Never leave a server request hanging. Interactive approvals are disabled
				// by default; unexpected requests fail explicitly rather than approving work.
				if err = writer.writeJSON(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32601, "message": "agentuse does not support this server request: " + frame.Method}}); err != nil {
					return Result{Error: err.Error(), SessionID: thread}
				}
				continue
			}
			if len(frame.ID) > 0 {
				var id int64
				if json.Unmarshal(frame.ID, &id) != nil {
					continue
				}
				p, found := pending[id]
				if !found {
					continue
				}
				delete(pending, id)
				if p.method == "turn/steer" {
					steering = false
					var ack struct {
						TurnID string `json:"turnId"`
					}
					_ = json.Unmarshal(frame.Result, &ack)
					if frame.Error != nil || ack.TurnID != turn {
						in.steerQueue().restore(p.steers)
						steerRejected = true
						reason := "turnId 不匹配"
						if frame.Error != nil {
							reason = frame.Error.Message
						}
						report("progress", map[string]string{"text": "Codex 未接收本轮插话，保留为后续任务: " + reason})
					} else {
						for _, s := range p.steers {
							report("steered", protocol.SteeredPayload{SteerID: s.ID})
						}
					}
					continue
				}
				if frame.Error != nil {
					return Result{Error: fmt.Sprintf("Codex %s: %s", p.method, frame.Error.Message), SessionID: thread}
				}
				switch p.method {
				case "initialize":
					if err = writer.writeJSON(map[string]any{"method": "initialized", "params": map[string]any{}}); err == nil {
						err = request("thread/start", map[string]any{"cwd": dir}, nil)
					}
				case "thread/start":
					var v struct {
						Thread struct {
							ID string `json:"id"`
						} `json:"thread"`
					}
					_ = json.Unmarshal(frame.Result, &v)
					thread = v.Thread.ID
					result.SessionID = thread
					if thread == "" {
						return Result{Error: "Codex thread/start 未返回 thread.id"}
					}
					err = request("turn/start", map[string]any{"threadId": thread, "input": codexText(input.Prompt)}, nil)
				case "turn/start":
					var v struct {
						Turn struct {
							ID string `json:"id"`
						} `json:"turn"`
					}
					_ = json.Unmarshal(frame.Result, &v)
					if v.Turn.ID == "" || (turn != "" && turn != v.Turn.ID) {
						return Result{Error: "Codex turn/start 返回无效 turn.id", SessionID: thread}
					}
					turn = v.Turn.ID
				}
				if err != nil {
					return Result{Error: err.Error(), SessionID: thread}
				}
				continue
			}
			var v struct {
				TokenUsage *struct {
					Total struct {
						Input  int64 `json:"inputTokens"`
						Output int64 `json:"outputTokens"`
						Cached int64 `json:"cachedInputTokens"`
					} `json:"total"`
				} `json:"tokenUsage"`

				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
				Item struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Command string `json:"command"`
				} `json:"item"`
				Plan []struct {
					Step   string `json:"step"`
					Status string `json:"status"`
				} `json:"plan"`
			}
			if json.Unmarshal(frame.Params, &v) != nil || thread == "" || v.ThreadID != thread {
				continue
			}
			if v.TurnID != "" && turn != "" && v.TurnID != turn {
				continue
			}
			switch frame.Method {
			case "thread/tokenUsage/updated":
				if v.TokenUsage != nil {
					usage := v.TokenUsage.Total
					result.Usage = &agentuse.Usage{InputTokens: usage.Input, OutputTokens: usage.Output, CacheReadTokens: usage.Cached}
				}
			case "turn/started":
				if turn == "" {
					turn = v.Turn.ID
				}
			case "item/started":
				if v.Item.Type == "commandExecution" {
					report("progress", map[string]string{"text": "🔧 " + v.Item.Command})
				}
			case "item/completed":
				if v.Item.Type == "agentMessage" {
					result.Output = v.Item.Text
					report("progress", map[string]string{"text": v.Item.Text})
				}
			case "turn/plan/updated":
				steps := make([]map[string]string, 0, len(v.Plan))
				for _, step := range v.Plan {
					status := step.Status
					if status == "inProgress" {
						status = "in_progress"
					}
					steps = append(steps, map[string]string{"text": step.Step, "status": status})
				}
				report("plan", map[string]any{"steps": steps})
			case "turn/completed":
				if turn != "" && v.Turn.ID != turn {
					continue
				}
				completed = true
				result.OK = v.Turn.Status == "completed"
				if !result.OK {
					result.Error = "Codex turn " + v.Turn.Status
					if v.Turn.Error != nil {
						result.Error = v.Turn.Error.Message
					}
				}
			}
		}
	}
}

// Keep rejected/unacknowledged input available for the server's follow-up run.
func (q *steerQueue) restore(items []protocol.Steer) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(items, q.items...)
}
