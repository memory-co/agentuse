// Conversation attachment publication coordinates metadata and byte storage through file.Service.
package service

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/memory-co/agentuse/internal/attachment"
)

type conversationAttachmentLimits struct{ MaxBytes, ConversationMaxBytes int64 }

func (s *Service) ConfigureConversationAttachments(max, total int64, logger *log.Logger) {
	s.conversationAttachmentLimits = conversationAttachmentLimits{max, total}
	if logger != nil {
		s.log = logger
	}
}

func (s *Service) OpenAttachmentFile(ctx context.Context, conversation, id string) (io.ReadCloser, error) {
	s.conversationFilesMu.RLock()
	defer s.conversationFilesMu.RUnlock()
	metadata, err := s.GetAttachment(ctx, conversation, id)
	if err != nil {
		return nil, err
	}
	if s.Files == nil {
		return nil, fmt.Errorf("file storage unavailable")
	}
	stream, size, err := s.Files.Get(ctx, conversationFileNamespace, conversation+"/"+metadata.RelativePath)
	if err != nil {
		return nil, err
	}
	if size != metadata.SizeBytes {
		stream.Close()
		return nil, fmt.Errorf("stored attachment size differs")
	}
	return stream, nil
}

func (s *Service) PublishAttachment(ctx context.Context, a AttachmentAccess, v *attachment.Attachment, temp string) error {
	if v.SizeBytes < 0 || v.SizeBytes > s.conversationAttachmentLimits.MaxBytes {
		return ErrAttachmentQuota
	}
	if s.Files == nil {
		return fmt.Errorf("file storage unavailable")
	}
	s.conversationFilesMu.Lock()
	defer s.conversationFilesMu.Unlock()
	published := false
	err := s.CommitAttachment(ctx, a, v, s.conversationAttachmentLimits.ConversationMaxBytes, func() error {
		source, err := os.Open(temp)
		if err != nil {
			return err
		}
		defer source.Close()
		if err = s.Files.Put(ctx, conversationFileNamespace, v.ConversationID+"/"+v.RelativePath, source, true); err != nil {
			return err
		}
		published = true
		return nil
	})
	if err != nil && published {
		if cleanup := s.Files.Delete(ctx, conversationFileNamespace, v.ConversationID+"/"+v.RelativePath); cleanup != nil {
			s.log.Printf("attachment publish cleanup: %v", cleanup)
		}
	}
	return err
}

func (s *Service) DeleteAttachment(ctx context.Context, a AttachmentAccess, conversation, id string) error {
	s.conversationFilesMu.Lock()
	defer s.conversationFilesMu.Unlock()
	metadata, err := s.GetAttachment(ctx, conversation, id)
	if err != nil {
		return err
	}
	if err = s.deleteAttachment(ctx, a, conversation, id); err != nil {
		return err
	}
	if s.Files != nil {
		if err = s.Files.Delete(ctx, conversationFileNamespace, conversation+"/"+metadata.RelativePath); err != nil {
			s.log.Printf("attachment cleanup: %v", err)
		}
	}
	return nil
}
