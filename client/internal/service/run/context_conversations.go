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
