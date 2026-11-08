package run

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func openAttachmentDir(root *os.Root, name string) (*os.Root, error) {
	if err := root.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	st, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("附件目录不能是符号链接: %s", name)
	}
	return root.OpenRoot(name)
}
func (d *endpointConnection) syncAttachments(ctx context.Context, m *protocol.ConversationMessage, dir string) error {
	c := agentuse.New(d.cfg.Server, d.cfg.Key)
	list := make([]agentuse.Attachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		list = append(list, a.Attachment)
	}
	return cacheContextAttachments(ctx, c, m.ConversationID, dir, list)
}

var errAttachmentNotCached = errors.New("attachment is not cached")

func cacheContextAttachments(ctx context.Context, c *agentuse.Client, conversation, dir string, list []agentuse.Attachment) error {
	if len(list) == 0 {
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	managed, err := openAttachmentDir(root, ".agentuse")
	if err != nil {
		return err
	}
	defer managed.Close()
	cache, err := openAttachmentDir(managed, "attachments")
	if err != nil {
		return err
	}
	defer cache.Close()
	for _, a := range list {
		name, e := attachment.Filename(a.Filename)
		if e != nil || name != a.Filename || !attachment.ValidID(a.ID, "at_") || a.SessionID != conversation {
			return fmt.Errorf("服务返回不合法的附件元数据")
		}
		sub, e := openAttachmentDir(cache, a.ID)
		if e != nil {
			return e
		}
		e = func() error {
			defer sub.Close()
			if info, e := sub.Lstat(name); e == nil {
				if !info.Mode().IsRegular() {
					return fmt.Errorf("附件缓存不是普通文件")
				}
				f, e := sub.Open(name)
				if e != nil {
					return e
				}
				e = agentuse.CopyVerified(discardWriter{}, f, a)
				f.Close()
				if e == nil {
					return nil
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
			if c == nil {
				return errAttachmentNotCached
			}
			src, e := c.DownloadAttachment(ctx, "", a.SessionID, a.ID)
			if e != nil {
				return e
			}
			defer src.Close()
			tmp := ".download-" + uuid.NewString()
			f, e := sub.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			defer sub.Remove(tmp)
			defer f.Close()
			if e = agentuse.CopyVerified(f, src, a); e != nil {
				return e
			}
			if e = f.Sync(); e != nil {
				return e
			}
			if e = f.Close(); e != nil {
				return e
			}
			return renameAttachment(sub, tmp, name)
		}()
		if e != nil {
			return e
		}
	}
	return nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
