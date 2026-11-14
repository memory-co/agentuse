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
	if source.AuthorRole == core.RoleChildConversationMsg {
		instructions += fmt.Sprintf("\n# Child conversation round finished\nThis notification is from the child conversation, not an Agent speaking in this conversation. Read the completed interval with context conversation read %s --round %d before responding. Files remain in that child's tree; use context attachment list --conversation %s and context attachment download PATH --conversation %s.\n", source.AuthorConversationID, source.AuthorRoundSeq, source.AuthorConversationID, source.AuthorConversationID)
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
	if err := s.ReconcileRoundNotifications(ctx); err != nil {
		s.Log.Printf("round notification: %v", err)
	}
}

const coordinatorPolicy = `# Conversation execution policy: coordinator
You coordinate this top-level conversation. Do not implement, research, or execute
the delegated work yourself, even for a small task. Analyze the request, use
context conversation agents to select executors, and context conversation create
to delegate execution into child conversations. Keep each child ID. Answer child
questions with context conversation send CHILD_ID --agents IDS MESSAGE, review
results, and consolidate the response. Server sends one child_conversation notification
per completed child round. Read it with context conversation read CHILD_ID --round N;
a completed round may contain questions or failed executions and is not proof of success. Only you may create execution children.
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
another conversation. If you need clarification, resources, or a changed assignment,
explain the question or obstacle in your final response and end this round normally.
Do not wait for a coordinator reply during the same round. Server notifies the
creating parent Agent only after every input and Agent execution in this round has
settled. The coordinator may send instructions for a later round. Do not send a
separate parent message or a duplicate report.
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
