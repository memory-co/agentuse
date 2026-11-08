package run

import (
	"fmt"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// completeMessagesText renders background conversation as a readable transcript.
// Transport IDs, sequence numbers and file verification metadata stay in the API.
func completeMessagesText(messages []agentuse.CompleteMessage) string {
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n# complete_messages\n\nEarlier conversation messages, in order. Use them as background; do not repeat earlier tasks.\n")
	for _, m := range messages {
		role := "User"
		if m.AuthorRole == "agent" {
			role = "Agent"
		}
		fmt.Fprintf(&b, "\n## %s %s", role, m.AuthorID)
		for _, id := range m.ToAgentIDs {
			fmt.Fprintf(&b, " @%s", id)
		}
		for _, id := range m.ToUserIDs {
			fmt.Fprintf(&b, " @user:%s", id)
		}
		b.WriteString("\n\n")
		for _, line := range strings.Split(m.Text, "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
		if len(m.Attachments) > 0 {
			b.WriteString("\nAttachments (use context attachment read FILE_ID to fetch):\n")
			for _, a := range m.Attachments {
				fmt.Fprintf(&b, "- %s (file ID: %s)", a.Filename, a.ID)
				if a.RelativePath != "" {
					fmt.Fprintf(&b, "; path: %s", a.RelativePath)
				}
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}
