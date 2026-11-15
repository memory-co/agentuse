package test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/memory-co/agentuse/internal/attachment"
	conversationservice "github.com/memory-co/agentuse/server/internal/service/conversation"
)

func TestPublishAttachmentSingleFileLimit(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	s.ConversationService.ConfigureConversationAttachments(3, 100, nil)
	temp := filepath.Join(dir, "incoming")
	if err := os.WriteFile(temp, []byte("four"), 0600); err != nil {
		t.Fatal(err)
	}
	err := s.ConversationService.PublishAttachment(context.Background(), conversationservice.AttachmentAccess{}, &attachment.Attachment{SizeBytes: 4}, temp)
	if !errors.Is(err, conversationservice.ErrAttachmentQuota) {
		t.Fatal(err)
	}
	if _, err = os.Stat(temp); err != nil {
		t.Fatal("rejected publication moved the source", err)
	}
}
