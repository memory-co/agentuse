package controller

import (
	"context"
	"io"
	"strings"
	"testing"

	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestAttachmentPublicationSharesConversationNamespaceAndProtectsReferences(t *testing.T) {
	f := attachmentEnv(t, 4096, 16384)
	ctx := context.Background()
	path := "user_input/report.txt"
	if err := f.e.st.Files.Put(ctx, "conversations", f.conversation.ID+"/"+path, strings.NewReader("raw file"), true); err != nil {
		t.Fatal(err)
	}
	client := sdk.New(f.e.ts.URL, f.token)
	metadata, err := client.UploadAttachment(ctx, "", f.conversation.ID, "report.txt", strings.NewReader("attachment"))
	if err != nil || metadata.RelativePath != "user_input/report (2).txt" {
		t.Fatal("raw namespace collision was ignored", metadata, err)
	}
	if _, err = client.SendMessage(ctx, "", f.conversation.ID, sdk.MessageRequest{Prompt: "keep this file", AttachmentIDs: []string{metadata.ID}}); err != nil {
		t.Fatal(err)
	}
	if code := f.e.code(f.token, "PUT", "/api/conversations/"+f.conversation.ID+"/files/"+metadata.RelativePath, "replace"); code != 409 {
		t.Fatal("raw file API overwrote a message attachment", code)
	}
	stream, _, err := f.e.st.Files.Get(ctx, "conversations", f.conversation.ID+"/"+metadata.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || string(data) != "attachment" {
		t.Fatal(string(data), err)
	}
}
