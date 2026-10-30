package attachment

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

type Client struct {
	Server, Token    string
	RunID, AttemptID string
	HTTPClient       *http.Client
}
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("agentuse: %d %s", e.Status, e.Message) }
func (c Client) request(ctx context.Context, method, path, ctype string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Server, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if c.RunID != "" {
		req.Header.Set("X-Agentuse-Run", c.RunID)
		req.Header.Set("X-Agentuse-Attempt", c.AttemptID)
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
		return nil, &Error{res.StatusCode, e.Error}
	}
	return res, nil
}
func (c Client) json(ctx context.Context, path string, out any) error {
	r, e := c.request(ctx, "GET", path, "", nil)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(out)
}
func Path(session string) string { return "/api/sessions/" + url.PathEscape(session) + "/attachments" }
func (c Client) List(ctx context.Context, session string) ([]Attachment, error) {
	out := []Attachment{}
	cursor := ""
	for {
		var p Page
		path := Path(session)
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		if err := c.json(ctx, path, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Attachments...)
		if p.NextCursor == nil {
			break
		}
		if *p.NextCursor == cursor {
			return nil, fmt.Errorf("重复的分页游标")
		}
		cursor = *p.NextCursor
	}
	return out, nil
}
func (c Client) Get(ctx context.Context, session, id string) (Attachment, error) {
	var a Attachment
	err := c.json(ctx, Path(session)+"/"+url.PathEscape(id), &a)
	return a, err
}
func (c Client) Upload(ctx context.Context, session, filename string, src io.Reader, messageID int64) (Attachment, error) {
	var a Attachment
	filename, err := Filename(filename)
	if err != nil {
		return a, err
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	defer pr.Close()
	go func() {
		var err error
		if messageID > 0 {
			err = mw.WriteField("message_id", strconv.FormatInt(messageID, 10))
		}
		if err == nil {
			var part io.Writer
			part, err = mw.CreateFormFile("file", filename)
			if err == nil {
				_, err = io.Copy(part, src)
			}
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	res, err := c.request(ctx, "POST", Path(session), mw.FormDataContentType(), pr)
	if err != nil {
		return a, err
	}
	defer res.Body.Close()
	err = json.NewDecoder(res.Body).Decode(&a)
	return a, err
}
func (c Client) Content(ctx context.Context, a Attachment) (io.ReadCloser, error) {
	res, err := c.request(ctx, "GET", Path(a.SessionID)+"/"+url.PathEscape(a.ID)+"/content", "", nil)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
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
func (c Client) Download(ctx context.Context, a Attachment, path string, force bool) error {
	src, err := c.Content(ctx, a)
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
