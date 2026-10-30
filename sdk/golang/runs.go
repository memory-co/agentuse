package agentuse

import (
	"context"
	"strconv"
	"time"
)

type Run struct {
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	MessageID       int64  `json:"message_id"`
	AgentID         string `json:"agent_id,omitempty"`
	AgentKey        string `json:"agent_key"`
	AgentName       string `json:"agent_name"`
	DaemonID        string `json:"daemon_id,omitempty"`
	Harness         string `json:"harness"`
	Status          string `json:"status"`
	AttemptID       string `json:"attempt_id,omitempty"`
	LeaseUntil      *int64 `json:"lease_until,omitempty"`
	ContextSeq      int64  `json:"context_seq"`
	ResultMessageID *int64 `json:"result_message_id,omitempty"`
	Error           string `json:"error,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}
type Delivery struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	AgentID   string `json:"agent_id,omitempty"`
	AgentKey  string `json:"agent_key"`
	RunID     string `json:"run_id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}
type RunDetail struct {
	Run    Run            `json:"run"`
	Events []SessionEvent `json:"events"`
}
type WaitOptions struct{ ReturnOnWaiting bool }

func (c *Client) GetRun(ctx context.Context, user, session, run string) (*RunDetail, error) {
	var d RunDetail
	err := c.do(ctx, user, "GET", "/api/sessions/"+esc(session)+"/runs/"+esc(run), nil, &d)
	return &d, err
}
func (c *Client) CancelRun(ctx context.Context, user, session, run string) (*Run, error) {
	var r Run
	err := c.do(ctx, user, "POST", "/api/sessions/"+esc(session)+"/runs/"+esc(run)+"/cancel", nil, &r)
	return &r, err
}
func (c *Client) UpgradeSession(ctx context.Context, user, session string) (*Session, error) {
	var s Session
	err := c.do(ctx, user, "POST", "/api/sessions/"+esc(session)+"/upgrade", nil, &s)
	return &s, err
}
func (c *Client) AnswerRunQuestion(ctx context.Context, user, session, run string, qid int64, answers map[string]string) (*Question, error) {
	var q Question
	err := c.do(ctx, user, "POST", "/api/sessions/"+esc(session)+"/runs/"+esc(run)+"/questions/"+strconv.FormatInt(qid, 10)+"/answer", map[string]any{"answers": answers}, &q)
	return &q, err
}
func terminal(s string) bool { return s == "succeeded" || s == "failed" || s == "cancelled" }
func (c *Client) WaitRun(ctx context.Context, user, session, run string, interval time.Duration, opts ...WaitOptions) (*RunDetail, error) {
	if interval <= 0 {
		interval = time.Second
	}
	for {
		d, e := c.GetRun(ctx, user, session, run)
		if e != nil {
			return nil, e
		}
		if terminal(d.Run.Status) || (d.Run.Status == "waiting" && len(opts) > 0 && opts[0].ReturnOnWaiting) {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return d, ctx.Err()
		case <-time.After(interval):
		}
	}
}
func (c *Client) WaitMessageRuns(ctx context.Context, user, session string, message int64, interval time.Duration) (*SessionDetail, error) {
	if interval <= 0 {
		interval = time.Second
	}
	for {
		d, e := c.GetSession(ctx, user, session)
		if e != nil {
			return nil, e
		}
		ready := true
		for _, delivery := range d.Deliveries {
			if delivery.MessageID == message && !terminal(delivery.Status) {
				ready = false
			}
		}
		if ready {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return d, ctx.Err()
		case <-time.After(interval):
		}
	}
}
