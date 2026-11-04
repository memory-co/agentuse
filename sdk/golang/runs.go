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
	SessionID       string          `json:"session_id"`
	FirstMessageID  int64           `json:"first_message_id"`
	AgentID         string          `json:"agent_id"`
	DaemonID        string          `json:"daemon_id"`
	ClientSessionID string          `json:"client_session_id"`
	Harness         string          `json:"harness"`
	HarnessRevision string          `json:"harness_revision"`
	Status          string          `json:"status"`
	ResultMessageID *int64          `json:"result_message_id,omitempty"`
	Output          string          `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

// Delivery exists before a client accepts input. RunID is empty until accepted.
type Delivery struct {
	AgentVersionID  string          `json:"agent_version_id"`
	ID              int64           `json:"id"`
	MessageID       int64           `json:"message_id"`
	SessionID       string          `json:"session_id"`
	AgentID         string          `json:"agent_id"`
	DaemonID        string          `json:"daemon_id"`
	Harness         string          `json:"harness"`
	Content         json.RawMessage `json:"content"`
	Status          string          `json:"status"`
	RunID           string          `json:"run_id,omitempty"`
	InputID         string          `json:"input_id,omitempty"`
	ClientSessionID string          `json:"client_session_id,omitempty"`
	Error           string          `json:"error,omitempty"`
}
type RunDetail struct {
	Run    Run            `json:"run"`
	Events []SessionEvent `json:"events"`
}
type WaitOptions struct{ ReturnOnWaiting bool }

func (c *Client) GetRun(ctx context.Context, as, conversation, run string) (*RunDetail, error) {
	var out RunDetail
	err := c.do(ctx, as, "GET", "/api/sessions/"+esc(conversation)+"/runs/"+esc(run), nil, &out)
	return &out, err
}
func (c *Client) CancelRun(ctx context.Context, as, conversation, run string) (*Run, error) {
	var out Run
	err := c.do(ctx, as, "POST", "/api/sessions/"+esc(conversation)+"/runs/"+esc(run)+"/cancel", map[string]any{}, &out)
	return &out, err
}
func (c *Client) ReplyRunRequest(ctx context.Context, as, conversation, run, request string, answer any) error {
	return c.do(ctx, as, "POST", "/api/sessions/"+esc(conversation)+"/runs/"+esc(run)+"/requests/"+esc(request)+"/reply", map[string]any{"answer": answer}, nil)
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
func (c *Client) WaitMessageRuns(ctx context.Context, as, conversation string, message int64, poll time.Duration) (*SessionDetail, error) {
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		out, err := c.GetSession(ctx, as, conversation)
		if err != nil {
			return out, err
		}
		done := true
		for _, d := range out.Deliveries {
			if d.MessageID == message && d.Status != "completed" && d.Status != "failed" && d.Status != "cancelled" {
				done = false
			}
		}
		if done {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
