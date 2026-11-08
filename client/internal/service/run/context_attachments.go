package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	client.HTTPClient = &http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return client, nil
}

// Outputs come from Server attribution, never a freshly enumerated conversation.
// Waiting here is read-only: native accepted can precede the Server's receipt.
func (c *ExecutionContext) outputAttachments(ctx context.Context) ([]sdk.Attachment, error) {
	if c.EndpointID == endpoint.LocalID {
		return []sdk.Attachment{}, nil
	}
	client, err := c.client()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Second)
	var files []sdk.Attachment
	for {
		files, err = client.AllRunAttachments(ctx, "", c.ConversationID, c.RunID)
		if !sdk.IsStatus(err, 404) || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.SessionID != c.ConversationID || file.CreatedByRunID != c.RunID || !attachment.ValidID(file.ID, "at_") {
			return nil, fmt.Errorf("Server 返回了其他上下文的输出附件")
		}
	}
	return files, nil
}

func (c *ExecutionContext) attachmentMetadata(a sdk.Attachment) ContextAttachment {
	out := ContextAttachment{ID: a.ID, RelativePath: a.RelativePath, Name: a.Filename, MIMEType: a.ContentType, SizeBytes: a.SizeBytes, SHA256: a.SHA256, Direction: "input", ConversationID: c.ConversationID,
		DownloadPath: "/api/sessions/" + url.PathEscape(c.ConversationID) + "/attachments/" + url.PathEscape(a.ID) + "/content"}
	if a.CreatedByRunID == c.RunID {
		out.Direction = "output"
		out.RunID = c.RunID
	}
	return out
}

func (c *ExecutionContext) Attachments(ctx context.Context) ([]ContextAttachment, error) {
	outputs, err := c.outputAttachments(ctx)
	if err != nil {
		return nil, err
	}
	out := []ContextAttachment{}
	seen := map[string]sdk.Attachment{}
	for _, group := range [][]sdk.Attachment{c.files, outputs} {
		for _, file := range group {
			if old, ok := seen[file.ID]; ok {
				if old != file {
					return nil, fmt.Errorf("附件元数据冲突")
				}
				continue
			}
			seen[file.ID] = file
			out = append(out, c.attachmentMetadata(file))
		}
	}
	return out, nil
}

func (c *ExecutionContext) Attachment(ctx context.Context, id string) (ContextAttachment, error) {
	var selected *sdk.Attachment
	for i := range c.files {
		if c.files[i].ID == id {
			selected = &c.files[i]
			break
		}
	}
	if selected == nil {
		outputs, err := c.outputAttachments(ctx)
		if err != nil {
			return ContextAttachment{}, err
		}
		for i := range outputs {
			if outputs[i].ID == id {
				selected = &outputs[i]
				break
			}
		}
	}
	if selected == nil {
		return ContextAttachment{}, fmt.Errorf("附件不属于当前 run 的输入或输出")
	}
	err := cacheContextAttachments(ctx, nil, c.ConversationID, c.Workspace, []sdk.Attachment{*selected})
	if errors.Is(err, errAttachmentNotCached) {
		client, e := c.client()
		if e != nil {
			return ContextAttachment{}, e
		}
		err = cacheContextAttachments(ctx, client, c.ConversationID, c.Workspace, []sdk.Attachment{*selected})
	}
	if err != nil {
		return ContextAttachment{}, err
	}
	path, err := fileutil.SafePath(c.Workspace, ".agentuse", "attachments", selected.ID, selected.Filename)
	if err != nil {
		return ContextAttachment{}, err
	}
	out := c.attachmentMetadata(*selected)
	out.LocalPath = path
	return out, nil
}

func (c *ExecutionContext) UploadAttachment(ctx context.Context, path string) (ContextAttachment, error) {
	if c.EndpointID == endpoint.LocalID {
		return ContextAttachment{}, fmt.Errorf("本地调试没有 Server，无法上传附件")
	}
	if c.Status != "running" && c.Status != "waiting" {
		return ContextAttachment{}, fmt.Errorf("run 已结束，无法上传输出附件")
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
	name, err := attachment.Filename(filepath.Base(path))
	if err != nil {
		return ContextAttachment{}, err
	}
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return ContextAttachment{}, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return ContextAttachment{}, err
	}
	if _, err = c.outputAttachments(ctx); err != nil {
		return ContextAttachment{}, err
	}
	client, err := c.client()
	if err != nil {
		return ContextAttachment{}, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ContextAttachment{}, err
	}
	relative, err := filepath.Rel(c.Workspace, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || strings.HasPrefix(relative, ".agentuse"+string(os.PathSeparator)) {
		relative = name
	}
	uploaded, err := client.UploadAttachmentAt(ctx, "", c.ConversationID, filepath.ToSlash(relative), file)
	if err != nil {
		return ContextAttachment{}, fmt.Errorf("上传失败；连接中断时可能已保存，请先用 context attachment list 核对: %w", err)
	}
	if uploaded.SessionID != c.ConversationID || uploaded.CreatedByRunID != c.RunID || !attachment.ValidID(uploaded.ID, "at_") {
		return ContextAttachment{}, fmt.Errorf("Server 返回的上传归属不匹配")
	}
	if uploaded.SizeBytes != size || uploaded.SHA256 != hex.EncodeToString(digest.Sum(nil)) {
		return ContextAttachment{}, fmt.Errorf("文件已保存为 %s，但上传内容与读取的源文件不一致；请先列出附件核对", uploaded.ID)
	}
	return c.attachmentMetadata(*uploaded), nil
}
