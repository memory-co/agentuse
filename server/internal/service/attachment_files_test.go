package service

import (
	"context"
	"errors"
	"github.com/memory-co/agentuse/internal/attachment"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishAttachmentSingleFileLimit(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	s.ConfigureAttachments(dir, 3, 100, nil)
	temp := filepath.Join(dir, "incoming")
	if err := os.WriteFile(temp, []byte("four"), 0600); err != nil {
		t.Fatal(err)
	}
	err := s.PublishAttachment(context.Background(), AttachmentAccess{}, attachment.Attachment{SizeBytes: 4}, temp)
	if !errors.Is(err, ErrAttachmentQuota) {
		t.Fatal(err)
	}
	if _, err = os.Stat(temp); err != nil {
		t.Fatal("rejected publication moved the source", err)
	}
}
