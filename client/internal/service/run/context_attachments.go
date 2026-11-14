package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/internal/attachment"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func (c *ExecutionContext) client() (*sdk.Client, error) {
	if c.manifest.ConfigPath == "" {
		return nil, fmt.Errorf("上下文缺少 endpoint 注册配置")
	}
	ep, err := (endpoint.Service{Path: c.manifest.ConfigPath}).GetByID(c.EndpointID)
	if err != nil {
		return nil, err
	}
	client := sdk.New(ep.ServerURL, ep.DeviceKey)
	client.RunID = c.RunID
	client.Source = c.fileSource
	client.HTTPClient = &http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return client, nil
}

func (c *ExecutionContext) conversationAttachments(ctx context.Context) ([]sdk.Attachment, error) {
	if c.EndpointID == endpoint.LocalID {
		return []sdk.Attachment{}, nil
	}
	client, err := c.client()
	if err != nil {
		return nil, err
	}
	files, err := client.AllAttachments(ctx, "", c.ConversationID)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if err = validateContextAttachment(c.ConversationID, file); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func (c *ExecutionContext) attachmentMetadata(a sdk.Attachment) ContextAttachment {
	out := ContextAttachment{ID: a.ID, RelativePath: a.RelativePath, Name: a.Filename, MIMEType: a.ContentType, SizeBytes: a.SizeBytes, SHA256: a.SHA256, Direction: "input", ConversationID: c.ConversationID,
		DownloadPath: "/api/conversations/" + url.PathEscape(c.ConversationID) + "/attachments/" + url.PathEscape(a.ID) + "/content"}
	if a.CreatedByRunID != "" {
		out.Direction = "output"
		out.RunID = a.CreatedByRunID
	}
	return out
}

func (c *ExecutionContext) Attachments(ctx context.Context) ([]ContextAttachment, error) {
	files, err := c.conversationAttachments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ContextAttachment, 0, len(files))
	for _, file := range files {
		out = append(out, c.attachmentMetadata(file))
	}
	return out, nil
}

func (c *ExecutionContext) DownloadAttachment(ctx context.Context, path string) (ContextAttachment, error) {
	relative, err := attachment.RelativePath(path)
	if err != nil {
		return ContextAttachment{}, err
	}
	if c.EndpointID == endpoint.LocalID {
		return ContextAttachment{}, fmt.Errorf("本地调试没有 Server，无法下载附件")
	}
	files, err := c.conversationAttachments(ctx)
	if err != nil {
		return ContextAttachment{}, err
	}
	for _, file := range files {
		if file.RelativePath != relative {
			continue
		}
		client, err := c.client()
		if err != nil {
			return ContextAttachment{}, err
		}
		local, err := materializeAttachment(ctx, client, c.ConversationID, c.Workspace, file, nil)
		if err != nil {
			return ContextAttachment{}, err
		}
		out := c.attachmentMetadata(file)
		out.LocalPath = local
		return out, nil
	}
	return ContextAttachment{}, fmt.Errorf("会话中没有路径为 %q 的附件", relative)
}

func (c *ExecutionContext) UploadAttachment(ctx context.Context, path string) (ContextAttachment, error) {
	if c.EndpointID == endpoint.LocalID {
		return ContextAttachment{}, fmt.Errorf("本地调试没有 Server，无法上传附件")
	}
	if c.Status != "running" && c.Status != "waiting" {
		return ContextAttachment{}, fmt.Errorf("run 已结束，无法上传输出附件")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.Workspace, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ContextAttachment{}, err
	}
	relative, err := filepath.Rel(c.Workspace, absolute)
	if err != nil {
		return ContextAttachment{}, err
	}
	if _, err = attachment.RelativePath(filepath.ToSlash(relative)); err != nil {
		return ContextAttachment{}, err
	}
	path, err = fileutil.SafePath(c.Workspace, strings.Split(filepath.ToSlash(relative), "/")...)
	if err != nil {
		return ContextAttachment{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ContextAttachment{}, err
	}
	if !info.Mode().IsRegular() {
		return ContextAttachment{}, fmt.Errorf("只能上传普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return ContextAttachment{}, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return ContextAttachment{}, err
	}
	if !info.Mode().IsRegular() {
		return ContextAttachment{}, fmt.Errorf("只能上传普通文件")
	}
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return ContextAttachment{}, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return ContextAttachment{}, err
	}
	client, err := c.client()
	if err != nil {
		return ContextAttachment{}, err
	}
	// Native accepted can precede its Server report. Reuse the read-only
	// source check; never retry a POST whose outcome could already be committed.
	if c.fileSource == nil {
		client, err = c.ConversationClient(ctx)
		if err != nil {
			return ContextAttachment{}, err
		}
	}
	uploaded, err := client.UploadAttachmentAt(ctx, "", c.ConversationID, filepath.ToSlash(relative), file)
	if err != nil {
		return ContextAttachment{}, fmt.Errorf("上传失败；连接中断时可能已保存，请先用 context attachment list 核对: %w", err)
	}
	if uploaded.ConversationID != c.ConversationID || uploaded.CreatedByRunID != c.RunID || !attachment.ValidID(uploaded.ID, "at_") {
		return ContextAttachment{}, fmt.Errorf("Server 返回的上传归属不匹配")
	}
	if uploaded.SizeBytes != size || uploaded.SHA256 != hex.EncodeToString(digest.Sum(nil)) {
		return ContextAttachment{}, fmt.Errorf("文件已保存为 %s，但上传内容与读取的源文件不一致；请先列出附件核对", uploaded.RelativePath)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return ContextAttachment{}, err
	}
	canonical, err := materializeAttachment(ctx, nil, c.ConversationID, c.Workspace, *uploaded, file)
	if err != nil {
		return ContextAttachment{}, fmt.Errorf("file published as %s but could not be saved at its conversation path: %w", uploaded.RelativePath, err)
	}
	out := c.attachmentMetadata(*uploaded)
	out.LocalPath = canonical
	return out, nil
}
