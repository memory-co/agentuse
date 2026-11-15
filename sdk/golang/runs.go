package agentuse

import (
	"context"
	"encoding/json"
	"time"
)

// Run is one client-reported harnessctl interaction; its ID is adapter-owned.
type Run struct {
	AgentVersionID  string          `json:"agent_version_id"`
	ID              string          `json:"id"`
	ConversationID  string          `json:"conversation_id"`
	FirstMessageID  string          `json:"first_message_id"`
	AgentID         string          `json:"agent_id"`
	DeviceID        string          `json:"device_id"`
	SessionID       string          `json:"session_id"`
	Harness         string          `json:"harness"`
	HarnessRevision string          `json:"harness_revision"`
	Model           *string         `json:"model" nullable:"true" doc:"Actual native model from result, including harness defaults; null until reported."`
	Usage           *RunUsage       `json:"usage" doc:"Native token measurements for this run; null if not reported."`
	Status          string          `json:"status"`
	ResultMessageID *string         `json:"result_message_id,omitempty"`
	Output          string          `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

// RunInput is an immutable Client-accepted input, not a pending delivery.
type RunInput struct {
	RunID     string `json:"run_id"`
	InputID   string `json:"input_id"`
	MessageID string `json:"message_id"`
	CreatedAt int64  `json:"created_at"`
}
type RunDetail struct {
	Inputs []RunInput          `json:"inputs" nullable:"false"`
	Run    Run                 `json:"run"`
	Events []ConversationEvent `json:"events"`
}
type WaitOptions struct{ ReturnOnWaiting bool }

func (c *Client) GetRun(ctx context.Context, as, conversation, run string) (*RunDetail, error) {
	var out RunDetail
	err := c.do(ctx, as, "GET", "/api/conversations/"+esc(conversation)+"/runs/"+esc(run), nil, &out)
	return &out, err
}
func (c *Client) CancelRun(ctx context.Context, as, conversation, run string) (*Run, error) {
	var out Run
	err := c.do(ctx, as, "POST", "/api/conversations/"+esc(conversation)+"/runs/"+esc(run)+"/cancel", map[string]any{}, &out)
	return &out, err
}
func (c *Client) ReplyRunRequest(ctx context.Context, as, conversation, run, request string, answer any) error {
	return c.do(ctx, as, "POST", "/api/conversations/"+esc(conversation)+"/runs/"+esc(run)+"/requests/"+esc(request)+"/reply", map[string]any{"answer": answer}, nil)
}
func (c *Client) WaitRun(ctx context.Context, as, conversation, run string, poll time.Duration, opts ...WaitOptions) (*RunDetail, error) {
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		out, err := c.GetRun(ctx, as, conversation, run)
		if err != nil {
			return out, err
		}
		s := out.Run.Status
		if s == "completed" || s == "failed" || s == "cancelled" || (s == "waiting" && len(opts) > 0 && opts[0].ReturnOnWaiting) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
func (c *Client) WaitMessageRuns(ctx context.Context, as, conversation string, message string, poll time.Duration) (*ConversationDetail, error) {
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		out, err := c.GetConversation(ctx, as, conversation)
		if err != nil {
			return out, err
		}
		if out.MessageSettled(message) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// MessageSettled waits for every Agent recipient, including before the first run.
func (d *ConversationDetail) MessageSettled(id string) bool {
	for _, message := range d.Messages {
		if message.ID != id {
			continue
		}
		runs := make(map[string]Run, len(d.Runs))
		for _, run := range d.Runs {
			runs[run.ID] = run
		}
		for _, agent := range message.ToAgentIDs {
			if receipt, ok := message.AgentErrors[agent]; ok && !receipt.Uncertain {
				continue
			}
			settled := false
			for _, input := range d.RunInputs {
				if input.MessageID != id {
					continue
				}
				if run, ok := runs[input.RunID]; ok && run.AgentID == agent {
					settled = run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled"
				}
			}
			if !settled {
				return false
			}
		}
		return true
	}
	return false
}
