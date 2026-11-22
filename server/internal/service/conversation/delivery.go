package conversation

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/service/run"
)

// prepareMessage assembles conversation context for a fixed delivery assignment.
func (s *Service) PrepareMessage(ctx context.Context, tx *gorm.DB, p run.MessageTarget) (protocol.ConversationMessage, error) {
	ss, err := s.LookupConversation(ctx, tx, agentuse.User{Role: agentuse.RoleAdmin}, p.ConversationID)
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	out := protocol.ConversationMessage{ConversationID: p.ConversationID, MessageID: p.MessageID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, Content: p.Content, Attachments: []attachment.Attachment{}}
	instructions := executionPolicy(ss)
	source, err := database.ScanMessage(database.Row(ctx, tx, database.MessageCols+` WHERE id=? AND conversation_id=?`, p.MessageID, p.ConversationID))
	if err != nil {
		return out, err
	}
	if source.AuthorRole == agentuse.RoleChildConversationMsg {
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
the delegated work yourself, even for a small task.
Each child conversation is dedicated to one original objective and its deliverables.
Before delegating, decide whether the request continues that original objective.
For every new independent objective, you must use context conversation create to
start a new child. Never repurpose an existing child for a different objective,
even when the Agent, repository, or tools are the same, or the child is idle or
has finished its work. You may assign the same Agent to multiple children.
Use context conversation agents to select executors. Record each child ID together
with its original objective. Use context conversation send CHILD_ID --agents IDS
MESSAGE only for clarification, additional requirements, corrections, review
feedback, or retries that serve that child's original objective. If the scope is
unclear, prefer a new child and include the relevant facts and artifact references
in its initial assignment instead of inheriting an unrelated execution context.
Review results and consolidate the response. Server sends one child_conversation notification
when a child round containing work you assigned completes. Read it with context conversation read CHILD_ID --round N;
a completed round may contain questions or failed executions and is not proof of success. Only you may create execution children.
After creating children or sending a follow-up for the original objective, briefly tell the user what you
delegated and end this run immediately. Do not wait for child completion, poll,
sleep, or repeatedly read/list to keep this run alive. Server will deliver a child
round notification and invoke you again if this run has ended. On that notification,
read the specified completed round and respond or send feedback about that child's
original objective. Create a new child for any new independent objective.
No Server identity or credentials need to be supplied to the context commands.
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
Do not wait for a coordinator reply during the same round. When a round includes
work assigned by the creating parent Agent, Server notifies that Agent after all
inputs and executions settle. For direct user requests, reply in this conversation.
The coordinator may send instructions for a later round. Do not send a separate
parent message or a duplicate report.
`

func executionPolicy(v agentuse.Conversation) string {
	if v.Mode == "collaborative" {
		return coordinatorPolicy
	}
	if v.ParentConversationID != "" {
		return executorPolicy
	}
	return singlePolicy
}
