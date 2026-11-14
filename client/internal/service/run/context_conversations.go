package run

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

// ConversationInfo is also available during preparation, before native acceptance.
func (c *ExecutionContext) ConversationInfo(ctx context.Context) (*sdk.ConversationDetail, error) {
	if c.EndpointID == endpoint.LocalID {
		return nil, fmt.Errorf("本地调试没有 Server conversation")
	}
	client, err := c.client()
	if err != nil {
		return nil, err
	}
	return client.GetConversation(ctx, "", c.ConversationID)
}

// ConversationClient waits only for the read-only source check. Mutation requests
// are never retried blindly when their outcome is unknown.
func (c *ExecutionContext) ConversationClient(ctx context.Context) (*sdk.Client, error) {
	if c.EndpointID == endpoint.LocalID {
		return nil, fmt.Errorf("本地调试没有 Server，无法协调 conversation")
	}
	if c.RunID == "" {
		return nil, fmt.Errorf("conversation 命令需要实际执行的 run")
	}
	client, err := c.client()
	if err != nil {
		return nil, err
	}
	client.Source = &sdk.ConversationSource{ConversationID: c.ConversationID, AgentID: c.AgentID, SessionID: c.SessionID, RunID: c.RunID}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err = client.GetConversation(ctx, "", c.ConversationID)
		if err == nil {
			return client, nil
		}
		var apiErr *sdk.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 403 || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type ConversationWait struct {
	Reason string                  `json:"reason"`
	Detail *sdk.ConversationDetail `json:"detail"`
}

// WaitChild fixes a conversation-local round when waiting begins. A later round
// must not delay or contaminate the results of that completed interval.
func (c *ExecutionContext) WaitChild(ctx context.Context, client *sdk.Client, id string, round int64, timeout time.Duration) (*ConversationWait, error) {
	if timeout <= 0 || round < 0 {
		return nil, fmt.Errorf("timeout must be positive and round cannot be negative")
	}
	parent, err := client.GetConversation(ctx, "", c.ConversationID)
	if err != nil {
		return nil, err
	}
	var baseline int64
	for _, m := range parent.Messages {
		if m.Seq > baseline {
			baseline = m.Seq
		}
	}
	child, err := client.GetConversation(ctx, "", id)
	if err != nil {
		return nil, err
	}
	if child.Conversation.ParentConversationID != c.ConversationID {
		return nil, fmt.Errorf("只能等待当前 conversation 的直接子会话")
	}
	if round == 0 {
		if len(child.Rounds) == 0 {
			return &ConversationWait{Reason: "no_round", Detail: child}, nil
		}
		round = child.Rounds[len(child.Rounds)-1].Seq
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		child, err = client.GetConversationRound(ctx, "", id, round)
		if err != nil {
			return nil, err
		}
		if len(child.Rounds) != 1 || child.Rounds[0].Seq != round {
			return nil, fmt.Errorf("Server returned an invalid round snapshot")
		}
		if child.Rounds[0].FinishedAt != nil {
			return &ConversationWait{Reason: "settled", Detail: child}, nil
		}
		for _, r := range child.Runs {
			if r.Status == "waiting" {
				return &ConversationWait{Reason: "needs_input", Detail: child}, nil
			}
		}
		parent, err = client.GetConversation(ctx, "", c.ConversationID)
		if err != nil {
			return nil, err
		}
		for _, m := range parent.Messages {
			if m.Seq > baseline {
				for _, a := range m.ToAgentIDs {
					if a == c.AgentID {
						return &ConversationWait{Reason: "new_input", Detail: child}, nil
					}
				}
			}
		}
		for _, d := range parent.Deliveries {
			if d.AgentID == c.AgentID && (d.Status == "pending" || d.Status == "sent" || d.Status == "unknown") {
				return &ConversationWait{Reason: "new_input", Detail: child}, nil
			}
		}
		for _, q := range parent.Questions {
			if q.RunID == c.RunID && q.State == "pending" {
				return &ConversationWait{Reason: "needs_input", Detail: child}, nil
			}
		}
		for _, r := range parent.Runs {
			if r.ID == c.RunID && r.Status != "running" && r.Status != "waiting" {
				return &ConversationWait{Reason: "run_ended", Detail: child}, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return &ConversationWait{Reason: "timeout", Detail: child}, nil
		case <-ticker.C:
		}
	}
}

// RelatedFiles keeps namespaces separate and downloads into an isolated directory.
// The Server remains authoritative about the allowed parent/child relationship.
func (c *ExecutionContext) RelatedFiles(ctx context.Context, target string) (*ExecutionContext, error) {
	client, err := c.ConversationClient(ctx)
	if err != nil {
		return nil, err
	}
	if target == "parent" {
		d, e := client.GetConversation(ctx, "", c.ConversationID)
		if e != nil {
			return nil, e
		}
		target = d.Conversation.ParentConversationID
	}
	if uuid.Validate(target) != nil {
		return nil, fmt.Errorf("需要相关 conversation ID 或 parent")
	}
	if target == c.ConversationID {
		return c, nil
	}
	workspace, err := fileutil.SafePath(c.Workspace, ".context", "conversations", target)
	if err != nil {
		return nil, err
	}
	out := *c
	out.ConversationID = target
	out.Workspace = filepath.Clean(workspace)
	out.fileSource = client.Source
	return &out, nil
}
