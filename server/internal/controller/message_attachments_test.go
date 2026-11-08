package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestMessagesKeepOnlyTheirSelectedAttachments(t *testing.T) {
	f := attachmentEnv(t, 4096, 16384)
	ctx := context.Background()
	client := sdk.New(f.e.ts.URL, f.token)
	a, err := client.UploadAttachment(ctx, "", f.conversation.ID, "first.txt", strings.NewReader("first file"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := client.UploadAttachment(ctx, "", f.conversation.ID, "second.txt", strings.NewReader("second file"))
	if err != nil {
		t.Fatal(err)
	}
	unused, err := client.UploadAttachment(ctx, "", f.conversation.ID, "unused.txt", strings.NewReader("not selected"))
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(0)
	accept := func(message protocol.ConversationMessage) {
		seq++
		run := uuid.NewString()
		event, _ := json.Marshal(map[string]any{"type": "accepted", "run_id": run, "input_id": run, "text": message.Content})
		if err := f.e.st.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{ConversationID: f.conversation.ID, SessionID: "native", AgentID: f.agent, AgentVersionID: message.AgentVersionID, Revision: "test", MessageID: message.MessageID, Seq: seq, Event: event}); err != nil {
			t.Fatal(err)
		}
	}
	for index, ids := range [][]string{{a.ID}, {b.ID}, nil, {a.ID, a.ID}} {
		reply, err := client.SendMessage(ctx, "", f.conversation.ID, sdk.MessageRequest{Prompt: "message", ToAgentIDs: []string{f.agent}, AttachmentIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		count := len(ids)
		if index == 3 {
			count = 1
		}
		if len(reply.Message.Attachments) != count {
			t.Fatal("message snapshot differs", reply.Message.Attachments)
		}
		delivery, err := f.e.st.DispatchMessage(ctx, f.d)
		if err != nil {
			t.Fatal(err)
		}
		if len(delivery.Attachments) != count {
			t.Fatal("conversation files leaked into a message", delivery.Attachments)
		}
		if count > 0 && (delivery.Attachments[0].ID != ids[0] || !strings.HasPrefix(delivery.Attachments[0].RelativePath, "user_input/")) {
			t.Fatal(delivery.Attachments)
		}
		accept(delivery)
	}
	other, err := client.CreateConversation(ctx, "", sdk.CreateConversationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.SendMessage(ctx, "", other.Conversation.ID, sdk.MessageRequest{Prompt: "foreign file", AttachmentIDs: []string{a.ID}}); !sdk.IsStatus(err, 404) {
		t.Fatal("cross-conversation attachment accepted", err)
	}
	if err = client.DeleteAttachment(ctx, "", f.conversation.ID, a.ID); !sdk.IsStatus(err, 409) {
		t.Fatal("referenced file deleted", err)
	}
	if err = client.DeleteAttachment(ctx, "", f.conversation.ID, unused.ID); err != nil {
		t.Fatal("unused file could not be removed", err)
	}
	fileOnly, err := client.SendMessage(ctx, "", f.conversation.ID, sdk.MessageRequest{AttachmentIDs: []string{b.ID}, ToAgentIDs: []string{f.agent}})
	if err != nil || len(fileOnly.Message.Attachments) != 1 {
		t.Fatal(fileOnly, err)
	}
	delivery, err := f.e.st.DispatchMessage(ctx, f.d)
	if err != nil || !delivery.Valid() || len(delivery.Attachments) != 1 {
		t.Fatal(delivery, err)
	}
}

func TestConversationPathsAndAgentReplyFiles(t *testing.T) {
	f := attachmentEnv(t, 4096, 16384)
	ctx := context.Background()
	user := sdk.New(f.e.ts.URL, f.token)
	for _, want := range []string{"user_input/docs/readme.txt", "user_input/docs/readme (2).txt"} {
		file, err := user.UploadAttachmentAt(ctx, "", f.conversation.ID, "docs/readme.txt", strings.NewReader("bytes"))
		if err != nil || file.RelativePath != want {
			t.Fatal(file, err)
		}
	}
	for _, bad := range []string{"../escape.txt", "/absolute.txt", "a/../escape.txt", "a//file.txt", `a\file.txt`} {
		if _, err := user.UploadAttachmentAt(ctx, "", f.conversation.ID, bad, strings.NewReader("bytes")); !sdk.IsStatus(err, 400) {
			t.Fatal("unsafe path accepted", bad, err)
		}
	}
	if _, err := user.SendMessage(ctx, "", f.conversation.ID, sdk.MessageRequest{Prompt: "make report", ToAgentIDs: []string{f.agent}}); err != nil {
		t.Fatal(err)
	}
	message, err := f.e.st.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	report := func(seq int64, event any) {
		data, _ := json.Marshal(event)
		if err := f.e.st.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{ConversationID: f.conversation.ID, SessionID: "native", AgentID: f.agent, AgentVersionID: message.AgentVersionID, Revision: "test", MessageID: message.MessageID, Seq: seq, Event: data}); err != nil {
			t.Fatal(err)
		}
	}
	report(1, map[string]any{"type": "accepted", "run_id": run, "input_id": "one", "text": message.Content})
	device := sdk.New(f.e.ts.URL, f.key)
	device.RunID = run
	output, err := device.UploadAttachmentAt(ctx, "", f.conversation.ID, "reports/2026/result.txt", strings.NewReader("report"))
	if err != nil || output.RelativePath != "reports/2026/result.txt" {
		t.Fatal(output, err)
	}
	if _, err = device.UploadAttachmentAt(ctx, "", f.conversation.ID, "user_input/forged.txt", strings.NewReader("report")); !sdk.IsStatus(err, 400) {
		t.Fatal("AI used the user input namespace", err)
	}
	if _, err = device.UploadAttachmentAt(ctx, "", f.conversation.ID, "leaf", strings.NewReader("file")); err != nil {
		t.Fatal(err)
	}
	if _, err = device.UploadAttachmentAt(ctx, "", f.conversation.ID, "leaf/child.txt", strings.NewReader("file")); !sdk.IsStatus(err, 409) {
		t.Fatal("file treated as a directory", err)
	}
	report(2, map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"one"}, "output": "report ready"})
	detail, err := user.GetConversation(ctx, "", f.conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	reply := detail.Messages[len(detail.Messages)-1]
	if reply.AuthorRole != "agent" || len(reply.Attachments) != 2 || !func() bool {
		for _, file := range reply.Attachments {
			if file.ID == output.ID {
				return true
			}
		}
		return false
	}() {
		t.Fatal("agent reply omitted run outputs", reply)
	}
	other, err := user.CreateConversation(ctx, "", sdk.CreateConversationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	separate, err := user.UploadAttachmentAt(ctx, "", other.Conversation.ID, "docs/readme.txt", strings.NewReader("separate"))
	if err != nil || separate.RelativePath != "user_input/docs/readme.txt" {
		t.Fatal("file trees shared paths", separate, err)
	}
}
