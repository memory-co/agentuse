package run

import (
	"strings"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestCompleteMessagesTextIsReadableBackground(t *testing.T) {
	if got := completeMessagesText(nil); got != "" {
		t.Fatal(got)
	}
	got := completeMessagesText([]agentuse.CompleteMessage{
		{ID: "hidden-message-id", Seq: 7, AuthorRole: "user", AuthorID: "alice", ToAgentIDs: []string{"aa", "bb"}, Text: "Can you read this file?\n請保留消息原文。", Attachments: []agentuse.Attachment{{ID: "file-1", Filename: "guide.pdf", RelativePath: "user_input/guide.pdf", SHA256: "hidden-digest"}}},
		{AuthorRole: "agent", AuthorID: "bb", ToUserIDs: []string{"alice"}, Text: "Yes, I can."},
	})
	for _, want := range []string{"## User alice @aa @bb", "> Can you read this file?\n> 請保留消息原文。", "guide.pdf (file ID: file-1); path: user_input/guide.pdf", "## Agent bb @user:alice", "> Yes, I can."} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	for _, hidden := range []string{"hidden-message-id", "hidden-digest", `"author_role"`, `"attachments"`} {
		if strings.Contains(got, hidden) {
			t.Fatalf("transport metadata leaked: %s", got)
		}
	}
	if strings.Index(got, "User alice") > strings.Index(got, "Agent bb") {
		t.Fatal("message order changed")
	}
}
