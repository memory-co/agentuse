package controller

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
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

func TestConversationFileReadsEnforceBusinessAccess(t *testing.T) {
	f := attachmentEnv(t, 4096, 16384)
	ctx := context.Background()
	path := "reports/result.txt"
	owner := service.AttachmentAccess{User: f.u, Token: f.token}
	if err := f.e.st.PutConversationFile(ctx, owner, f.conversation.ID, path, strings.NewReader("result")); err != nil {
		t.Fatal(err)
	}
	stream, size, err := f.e.st.OpenConversationFile(ctx, owner, f.conversation.ID, path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || size != int64(len(data)) || string(data) != "result" {
		t.Fatal(string(data), size, err)
	}
	stranger, token := f.e.user("stranger", service.RoleUser)
	for _, access := range []service.AttachmentAccess{
		{User: stranger, Token: token},
		{DeviceID: f.d.ID, Token: f.key}, // No message has been delivered to this device.
	} {
		if stream, _, err := f.e.st.OpenConversationFile(ctx, access, f.conversation.ID, path); !errors.Is(err, service.ErrNotFound) {
			if stream != nil {
				stream.Close()
			}
			t.Fatal("conversation file bypassed business authorization", err)
		}
	}
}
