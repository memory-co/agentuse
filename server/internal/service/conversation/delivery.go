package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

// prepareMessage assembles conversation context for a fixed delivery assignment.
func (s *Service) PrepareMessage(ctx context.Context, tx *sql.Tx, p core.Delivery) (protocol.ConversationMessage, error) {
	ss, err := s.LookupConversation(ctx, tx, core.User{Role: core.RoleAdmin}, p.ConversationID)
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	out := protocol.ConversationMessage{ConversationID: p.ConversationID, MessageID: p.MessageID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, Content: p.Content, Attachments: []attachment.Attachment{}}
	instructions := executionPolicy(ss)
	source, err := store.ScanMessage(tx.QueryRowContext(ctx, core.MessageCols+` WHERE id=? AND conversation_id=?`, p.MessageID, p.ConversationID))
	if err != nil {
		return out, err
	}
	if source.SourceConversationID != "" {
		instructions += fmt.Sprintf("\n# Child feedback provenance\nSource conversation: %s\nSource message: %s\nThis is a returned child message. Files remain in the child file tree. Use context attachment list --conversation %s and context attachment download PATH --conversation %s to inspect them.\n", source.SourceConversationID, source.SourceMessageID, source.SourceConversationID, source.SourceConversationID)
	}
	if ss.ParentConversationID != "" {
		instructions += fmt.Sprintf("\n# Parent conversation\nParent: %s\nTo inspect input files retained there, use context attachment list --conversation parent and context attachment download PATH --conversation parent.\n", ss.ParentConversationID)
	}
	for _, file := range source.Attachments {
		out.Attachments = append(out.Attachments, file)
	}
	if strings.TrimSpace(p.Content) == "" && len(source.Attachments) > 0 {
		out.Content = "Please review the attachments in this message."
	}

	if len(out.Attachments) > 0 {
		instructions += "\n# Current message attachments\nFiles are fetched on demand. Use context attachment download PATH with a conversation-relative path below to save a file into the workspace before reading it.\n"
		for _, file := range out.Attachments {
			instructions += fmt.Sprintf("- %s\n", file.RelativePath)
		}
	}
	// History is assembled by Server after Client selects its actual local session.
	out.Context = instructions
	return out, nil
}

// DeliveryChanged handles acknowledged transport facts after commit.
func (s *Service) DeliveryChanged(ctx context.Context, conversation string) {
	s.NotifyConversation(ctx, conversation)
	if err := s.recordRejectedFeedback(ctx, conversation); err != nil {
		s.Log.Printf("rejected feedback: %v", err)
	}
	if err := s.ReconcileParentMessages(ctx); err != nil {
		s.Log.Printf("parent delivery: %v", err)
	}
}

const coordinatorPolicy = `# Conversation execution policy: coordinator
You coordinate this top-level conversation. Do not implement, research, or execute
the delegated work yourself, even for a small task. Analyze the request, use
context conversation agents to select executors, and context conversation create
to delegate execution into child conversations. Keep each child ID. Answer child
questions with context conversation send CHILD_ID --agents IDS MESSAGE, review
results, and consolidate the response. Only you may create execution children.
Do not wait indefinitely while a child needs your decision. No Server identity or
credentials need to be supplied to the context commands.
`
const singlePolicy = `# Conversation execution policy: single
Execute the request yourself using the tools available here. This conversation is
in single mode. Do not create or delegate to other conversations.
`

const executorPolicy = `# Conversation execution policy: executor
You execute a task in a child conversation. Do the assigned work yourself with the
tools available here. Do not create another conversation or delegate the work into
another conversation. Use context conversation send --parent MESSAGE to ask for
clarification, report an obstacle, or request a changed assignment. Continue here
when the parent replies. Give the final result normally; Server forwards the final
message to the recorded parent Agent. Do not send a duplicate final report.
`

func executionPolicy(v core.Conversation) string {
	if v.Mode == "collaborative" {
		return coordinatorPolicy
	}
	if v.ParentConversationID != "" {
		return executorPolicy
	}
	return singlePolicy
}
