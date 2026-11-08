package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/attachment"
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
		return nil, fmt.Errorf("attachment directory must not be a symbolic link: %s", name)
	}
	return root.OpenRoot(name)
}
func validateContextAttachment(conversation string, a agentuse.Attachment) error {
	if a.ConversationID != conversation || !attachment.ValidID(a.ID, "at_") {
		return fmt.Errorf("attachment does not belong to this conversation")
	}
	_, err := attachment.RelativePath(a.RelativePath)
	return err
}

// materializeAttachment uses only the Server's conversation-relative path.
// Existing verified bytes are reused. Otherwise write a verified temporary file
// and atomically publish it; source is supplied directly after an upload.
func materializeAttachment(ctx context.Context, client *agentuse.Client, conversation, workspace string, a agentuse.Attachment, source io.Reader) (string, error) {
	if err := validateContextAttachment(conversation, a); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return "", err
	}
	parts := strings.Split(a.RelativePath, "/")
	for _, part := range parts[:len(parts)-1] {
		sub, e := openAttachmentDir(root, part)
		root.Close()
		if e != nil {
			return "", e
		}
		root = sub
	}
	defer root.Close()
	name := parts[len(parts)-1]
	local := filepath.Join(workspace, filepath.FromSlash(a.RelativePath))
	if info, e := root.Lstat(name); e == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("attachment path is not a regular file")
		}
		f, e := root.Open(name)
		if e != nil {
			return "", e
		}
		e = agentuse.CopyVerified(discardWriter{}, f, a)
		f.Close()
		if e == nil {
			return local, nil
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	if source == nil {
		if client == nil {
			return "", fmt.Errorf("attachment download requires a Server connection")
		}
		stream, e := client.DownloadAttachment(ctx, "", conversation, a.ID)
		if e != nil {
			return "", e
		}
		defer stream.Close()
		source = stream
	}
	tmp := ".download-" + uuid.NewString()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(tmp)
	defer f.Close()
	if err = agentuse.CopyVerified(f, source, a); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = renameAttachment(root, tmp, name); err != nil {
		return "", err
	}
	return local, nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
