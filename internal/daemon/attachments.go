package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
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
func (d *Daemon) syncAttachments(ctx context.Context, m *protocol.Dispatch, dir string) error {
	c := attachment.Client{Server: d.cfg.Server, Token: d.cfg.Key}
	list, err := c.List(ctx, m.SessionID)
	if err != nil {
		return err
	}
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
		if e != nil || name != a.Filename || !attachment.ValidID(a.ID, "at_") || a.SessionID != m.SessionID {
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
				e = attachment.CopyVerified(discardWriter{}, f, a)
				f.Close()
				if e == nil {
					return nil
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
			src, e := c.Content(ctx, a)
			if e != nil {
				return e
			}
			defer src.Close()
			tmp := ".download-" + strings.TrimPrefix(a.ID, "at_")
			// Remove a previous interrupted download, never follow it.
			if e = sub.Remove(tmp); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
			f, e := sub.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			defer sub.Remove(tmp)
			defer f.Close()
			if e = attachment.CopyVerified(f, src, a); e != nil {
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
		m.Attachments = append(m.Attachments, protocol.LocalAttachment{Attachment: a, Path: filepath.ToSlash(filepath.Join(".agentuse", "attachments", a.ID, name))})
	}
	return nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
