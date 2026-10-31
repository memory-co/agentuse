package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/internal/attachment"
	"log"
	"os"
	"path/filepath"
	"time"
)

type AttachmentConfig struct {
	Dir                       string
	MaxBytes, SessionMaxBytes int64
}

func (s *Service) ConfigureAttachments(dir string, max, total int64, logger *log.Logger) {
	s.attachments = AttachmentConfig{dir, max, total}
	if logger != nil {
		s.log = logger
	}
}
func (s *Service) AttachmentPath(session, id string) string {
	return filepath.Join(s.attachments.Dir, session, id, "content")
}
func (s *Service) OpenAttachmentFile(session, id string) (*os.File, error) {
	s.attachmentMu.RLock()
	defer s.attachmentMu.RUnlock()
	return os.Open(s.AttachmentPath(session, id))
}
func (s *Service) PublishAttachment(ctx context.Context, a AttachmentAccess, v attachment.Attachment, temp string) error {
	if v.SizeBytes < 0 || v.SizeBytes > s.attachments.MaxBytes {
		return ErrAttachmentQuota
	}
	s.attachmentMu.RLock()
	defer s.attachmentMu.RUnlock()
	final := s.AttachmentPath(v.SessionID, v.ID)
	err := s.CommitAttachment(ctx, a, v, s.attachments.SessionMaxBytes, func() error {
		if e := os.MkdirAll(filepath.Dir(final), 0700); e != nil {
			return e
		}
		return os.Rename(temp, final)
	})
	if err != nil {
		_ = os.RemoveAll(filepath.Dir(final))
	}
	return err
}
func (s *Service) DeleteAttachment(ctx context.Context, a AttachmentAccess, session, id string) error {
	if err := s.deleteAttachment(ctx, a, session, id); err != nil {
		return err
	}
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	if err := os.RemoveAll(filepath.Dir(s.AttachmentPath(session, id))); err != nil {
		s.log.Printf("attachment cleanup: %v", err)
	}
	return nil
}

// CleanupAttachments removes only aged, unreferenced files under generated IDs.
func (s *Service) CleanupAttachments(ctx context.Context) error {
	if !s.attachmentMu.TryLock() {
		return nil
	}
	defer s.attachmentMu.Unlock()
	entries, err := os.ReadDir(s.attachments.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, session := range entries {
		if session.Name() == ".tmp" {
			if !session.IsDir() {
				continue
			}
			files, e := os.ReadDir(filepath.Join(s.attachments.Dir, ".tmp"))
			if e != nil {
				return e
			}
			for _, f := range files {
				info, e := f.Info()
				if e == nil && info.ModTime().Before(cutoff) {
					os.Remove(filepath.Join(s.attachments.Dir, ".tmp", f.Name()))
				}
			}
			continue
		}
		if !session.IsDir() || !attachment.ValidID(session.Name(), "ss_") {
			continue
		}
		files, e := os.ReadDir(filepath.Join(s.attachments.Dir, session.Name()))
		if e != nil {
			return e
		}
		for _, f := range files {
			if !f.IsDir() || !attachment.ValidID(f.Name(), "at_") {
				continue
			}
			info, e := f.Info()
			if e != nil || !info.ModTime().Before(cutoff) {
				continue
			}
			exists, e := s.AttachmentExists(ctx, session.Name(), f.Name())
			if e != nil {
				return e
			}
			if !exists {
				if e = os.RemoveAll(filepath.Join(s.attachments.Dir, session.Name(), f.Name())); e != nil {
					return fmt.Errorf("cleanup: %w", e)
				}
			}
		}
	}
	return nil
}
