package agentuse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Attachment struct {
	CreatedByAgentKey string `json:"created_by_agent_key,omitempty"`
	CreatedByRunID    string `json:"created_by_run_id,omitempty"`
	ID                string `json:"id"`
	SessionID         string `json:"session_id"`
	Filename          string `json:"filename"`
	ContentType       string `json:"content_type"`
	SizeBytes         int64  `json:"size_bytes"`
	SHA256            string `json:"sha256"`
	CreatedBy         string `json:"created_by"`
	Source            string `json:"source"`
	DaemonID          string `json:"daemon_id,omitempty"`
	CreatedAt         int64  `json:"created_at"`
	DownloadPath      string `json:"download_path"`
}
type AttachmentPage struct {
	Attachments []Attachment `json:"attachments"`
	NextCursor  *string      `json:"next_cursor"`
}

// AttachmentDownload must be closed by the caller.
type AttachmentDownload struct {
	io.ReadCloser
	Header http.Header
}

func attachmentPath(session string) string { return "/api/sessions/" + esc(session) + "/attachments" }
func (c *Client) attachmentRequest(ctx context.Context, user, method, path, ctype string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	c.runHeaders(req)
	if user != "" {
		req.Header.Set(UserHeader, user)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		var e struct{ Error string }
		_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return nil, &Error{Status: res.StatusCode, Message: e.Error}
	}
	return res, nil
}
func (c *Client) UploadAttachment(ctx context.Context, user, session, filename string, src io.Reader) (*Attachment, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	defer pr.Close()
	go func() {
		part, err := mw.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.Copy(part, src)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	res, err := c.attachmentRequest(ctx, user, "POST", attachmentPath(session), mw.FormDataContentType(), pr)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var a Attachment
	err = json.NewDecoder(res.Body).Decode(&a)
	return &a, err
}
func (c *Client) ListAttachments(ctx context.Context, user, session string, limit int, cursor string) (*AttachmentPage, error) {
	v := url.Values{}
	if limit != 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	var p AttachmentPage
	err := c.do(ctx, user, "GET", attachmentPath(session)+"?"+v.Encode(), nil, &p)
	return &p, err
}
func (c *Client) GetAttachment(ctx context.Context, user, session, id string) (*Attachment, error) {
	var a Attachment
	err := c.do(ctx, user, "GET", attachmentPath(session)+"/"+esc(id), nil, &a)
	return &a, err
}
func (c *Client) DownloadAttachment(ctx context.Context, user, session, id string) (*AttachmentDownload, error) {
	r, e := c.attachmentRequest(ctx, user, "GET", attachmentPath(session)+"/"+esc(id)+"/content", "", nil)
	if e != nil {
		return nil, e
	}
	return &AttachmentDownload{ReadCloser: r.Body, Header: r.Header}, nil
}
func (c *Client) DeleteAttachment(ctx context.Context, user, session, id string) error {
	return c.do(ctx, user, "DELETE", attachmentPath(session)+"/"+esc(id), nil, nil)
}

func CopyVerified(dst io.Writer, src io.Reader, a Attachment) error {
	if a.SizeBytes < 0 || a.SizeBytes == int64(^uint64(0)>>1) {
		return fmt.Errorf("不合法的附件大小")
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, a.SizeBytes+1))
	if err != nil {
		return err
	}
	if n != a.SizeBytes || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("附件大小或 SHA-256 校验失败")
	}
	return nil
}

func (c *Client) DownloadAttachmentFile(ctx context.Context, user string, a Attachment, path string, force bool) error {
	src, err := c.DownloadAttachment(ctx, user, a.SessionID, a.ID)
	if err != nil {
		return err
	}
	defer src.Close()
	f, err := os.CreateTemp(filepath.Dir(path), ".agentuse-download-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = CopyVerified(f, src, a); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if force {
		return os.Rename(f.Name(), path)
	}
	// Link fails atomically when output exists, unlike a check followed by rename.
	return os.Link(f.Name(), path)
}

func (c *Client) AllAttachments(ctx context.Context, user, session string) ([]Attachment, error) {
	out := []Attachment{}
	cursor := ""
	for {
		p, e := c.ListAttachments(ctx, user, session, 100, cursor)
		if e != nil {
			return nil, e
		}
		out = append(out, p.Attachments...)
		if p.NextCursor == nil {
			return out, nil
		}
		if *p.NextCursor == cursor {
			return nil, fmt.Errorf("重复的分页游标")
		}
		cursor = *p.NextCursor
	}
}
