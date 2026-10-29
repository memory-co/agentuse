package agentuse

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Attachment struct {
	ID           string `json:"id"`
	SessionID    string `json:"session_id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	CreatedBy    string `json:"created_by"`
	Source       string `json:"source"`
	DaemonID     string `json:"daemon_id,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	DownloadPath string `json:"download_path"`
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
