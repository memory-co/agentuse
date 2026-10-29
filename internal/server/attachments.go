package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/store"
)

type attachmentKey struct{}

func attachmentAccess(r *http.Request) store.AttachmentAccess {
	return r.Context().Value(attachmentKey{}).(store.AttachmentAccess)
}
func (s *Server) attachmentAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := requestToken(r)
		finish := func(w http.ResponseWriter, r *http.Request) {
			a := store.AttachmentAccess{User: currentUser(r), Token: token}
			if wk := workScope(r); wk != nil {
				a.WorkID = wk.ID
			}
			h(w, r.WithContext(context.WithValue(r.Context(), attachmentKey{}, a)))
		}
		if !strings.HasPrefix(token, "dk_") {
			s.workAuth(finish)(w, r)
			return
		}
		d, err := s.store.DaemonByKey(r.Context(), token)
		if err != nil {
			writeErr(w, 401, "daemon 密钥无效")
			return
		}
		if r.Header.Get(actAsHeader) != "" {
			writeErr(w, 403, "daemon 不能代为用户")
			return
		}
		if r.Method == http.MethodDelete {
			writeErr(w, 403, "daemon 不能删除附件")
			return
		}
		u, err := s.store.GetUser(r.Context(), d.UserID)
		if err != nil {
			s.attachmentErr(w, err)
			return
		}
		a := store.AttachmentAccess{User: u, DaemonID: d.ID, Token: token}
		h(w, r.WithContext(context.WithValue(r.Context(), attachmentKey{}, a)))
	}
}
func (s *Server) attachmentErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAttachmentUnauthorized):
		writeErr(w, 401, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, "会话或附件不存在")
	case errors.Is(err, store.ErrStaleMessage), errors.Is(err, store.ErrAttachmentCapability):
		writeErr(w, 409, err.Error())
	case errors.Is(err, store.ErrAttachmentQuota):
		writeErr(w, 413, err.Error())
	default:
		s.log.Printf("attachment: %v", err)
		writeErr(w, 500, "附件存储失败")
	}
}
func (s *Server) attachmentSession(w http.ResponseWriter, r *http.Request, upload bool) bool {
	id := r.PathValue("id")
	if !attachment.ValidID(id, "ss_") {
		writeErr(w, 404, "会话不存在")
		return false
	}
	a := attachmentAccess(r)
	if a.WorkID != "" {
		ss, err := s.store.GetSession(r.Context(), a.User, id)
		if err != nil {
			s.attachmentErr(w, err)
			return false
		}
		if ss.WorkID != a.WorkID {
			writeErr(w, 403, "工作令牌只能访问本工作的附件")
			return false
		}
	}
	if _, err := s.store.AttachmentSession(r.Context(), a, id, upload); err != nil {
		s.attachmentErr(w, err)
		return false
	}
	return true
}
func (s *Server) attachmentPath(session, id string) string {
	return filepath.Join(s.cfg.AttachmentsDir, session, id, "content")
}

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	// Authenticate the session before parsing potentially large request bodies.
	if !s.attachmentSession(w, r, false) {
		return
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "multipart/form-data" {
		writeErr(w, 415, "需要 multipart/form-data")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.AttachmentMaxBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, 400, "multipart 格式不合法")
		return
	}
	s.attachmentMu.RLock()
	defer s.attachmentMu.RUnlock()
	tmpdir := filepath.Join(s.cfg.AttachmentsDir, ".tmp")
	if err = os.MkdirAll(tmpdir, 0700); err != nil {
		s.attachmentErr(w, err)
		return
	}
	f, err := os.CreateTemp(tmpdir, "upload-")
	if err != nil {
		s.attachmentErr(w, err)
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	var filename, override, ctype, digest string
	var size int64
	a := attachmentAccess(r)
	seen := map[string]bool{}
	bad := func(err error) {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			writeErr(w, 413, "上传请求过大")
		} else {
			writeErr(w, 400, "multipart 格式不合法")
		}
	}
	for {
		part, e := mr.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			bad(e)
			return
		}
		name := part.FormName()
		if seen[name] || (name != "file" && name != "filename" && name != "message_id") {
			writeErr(w, 400, "未知或重复的 multipart 字段")
			return
		}
		seen[name] = true
		if name == "file" {
			// Part.FileName strips only platform-specific separators; Filename handles both.
			_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if e != nil {
				bad(e)
				return
			}
			filename = params["filename"]
			if filename == "" {
				writeErr(w, 400, "file 必须包含文件名")
				return
			}
			head := make([]byte, 512)
			n, e := io.ReadFull(part, head)
			if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
				bad(e)
				return
			}
			head = head[:n]
			ctype = http.DetectContentType(head)
			h := sha256.New()
			dst := io.MultiWriter(f, h)
			if _, err = dst.Write(head); err != nil {
				s.attachmentErr(w, err)
				return
			}
			rest, e := io.Copy(dst, io.LimitReader(part, s.cfg.AttachmentMaxBytes+1-int64(n)))
			size = int64(n) + rest
			if e != nil {
				var mb *http.MaxBytesError
				if errors.As(e, &mb) {
					bad(e)
				} else {
					s.attachmentErr(w, e)
				}
				return
			}
			if size > s.cfg.AttachmentMaxBytes {
				writeErr(w, 413, "文件超过大小限制")
				return
			}
			digest = hex.EncodeToString(h.Sum(nil))
		} else {
			if part.FileName() != "" {
				writeErr(w, 400, "文本字段不能是文件")
				return
			}
			b, e := io.ReadAll(io.LimitReader(part, 4097))
			if e != nil {
				bad(e)
				return
			}
			if len(b) > 4096 {
				writeErr(w, 400, "字段过长")
				return
			}
			if name == "filename" {
				override = string(b)
			} else {
				if a.DaemonID == "" {
					writeErr(w, 400, "此身份不能传 message_id")
					return
				}
				a.MessageID, e = strconv.ParseInt(string(b), 10, 64)
				if e != nil || a.MessageID <= 0 {
					writeErr(w, 400, "message_id 不合法")
					return
				}
			}
		}
		part.Close()
	}
	if !seen["file"] {
		writeErr(w, 400, "缺少 file")
		return
	}
	if a.DaemonID != "" && !seen["message_id"] {
		writeErr(w, 400, "daemon 上传需要 message_id")
		return
	}
	if seen["filename"] {
		filename = override
	}
	filename, err = attachment.Filename(filename)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err = f.Sync(); err != nil {
		s.attachmentErr(w, err)
		return
	}
	if err = f.Close(); err != nil {
		s.attachmentErr(w, err)
		return
	}
	v := attachment.Attachment{ID: s.store.NewAttachmentID(), SessionID: r.PathValue("id"), Filename: filename, ContentType: ctype, SizeBytes: size, SHA256: digest, CreatedBy: a.User.ID, Source: "user", CreatedAt: time.Now().Unix()}
	if a.DaemonID != "" {
		v.Source = "daemon"
		v.DaemonID = a.DaemonID
	} else if a.WorkID != "" {
		v.Source = "work"
	}
	final := s.attachmentPath(v.SessionID, v.ID)
	err = s.store.CommitAttachment(r.Context(), a, v, s.cfg.AttachmentSessionMaxBytes, func() error {
		if e := os.MkdirAll(filepath.Dir(final), 0700); e != nil {
			return e
		}
		return os.Rename(tmp, final)
	})
	if err != nil {
		os.RemoveAll(filepath.Dir(final))
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 409, "上传期间会话或权限发生变化")
			return
		}
		s.attachmentErr(w, err)
		return
	}
	v.DownloadPath = "/api/sessions/" + v.SessionID + "/attachments/" + v.ID + "/content"
	w.Header().Set("Location", strings.TrimSuffix(v.DownloadPath, "/content"))
	writeJSON(w, 201, v)
}
func (s *Server) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentSession(w, r, false) {
		return
	}
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		v, e := strconv.Atoi(q)
		if e != nil || v < 1 || v > 100 {
			writeErr(w, 400, "limit 必须在 1–100 之间")
			return
		}
		limit = v
	}
	page, err := s.store.ListAttachments(r.Context(), r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		if errors.Is(err, store.ErrAttachmentCursor) {
			writeErr(w, 400, err.Error())
		} else {
			s.attachmentErr(w, err)
		}
		return
	}
	writeJSON(w, 200, page)
}
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentSession(w, r, false) {
		return
	}
	v, err := s.store.GetAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachment_id"))
	if err != nil {
		s.attachmentErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleAttachmentContent(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentSession(w, r, false) {
		return
	}
	v, err := s.store.GetAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachment_id"))
	if err != nil {
		s.attachmentErr(w, err)
		return
	}
	s.attachmentMu.RLock()
	f, err := os.Open(s.attachmentPath(v.SessionID, v.ID))
	s.attachmentMu.RUnlock()
	if err != nil {
		s.attachmentErr(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", v.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(v.SizeBytes, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": v.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(200)
	if _, err = io.Copy(w, f); err != nil {
		s.log.Printf("attachment download: %v", err)
	}
}
func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentSession(w, r, false) {
		return
	}
	if err := s.store.DeleteAttachment(r.Context(), attachmentAccess(r), r.PathValue("id"), r.PathValue("attachment_id")); err != nil {
		s.attachmentErr(w, err)
		return
	}
	s.attachmentMu.Lock()
	err := os.RemoveAll(filepath.Dir(s.attachmentPath(r.PathValue("id"), r.PathValue("attachment_id"))))
	s.attachmentMu.Unlock()
	if err != nil {
		s.log.Printf("attachment cleanup: %v", err)
	}
	w.WriteHeader(204)
}

// CleanupAttachments removes only aged, unreferenced files under generated IDs.
func (s *Server) CleanupAttachments(ctx context.Context) error {
	if !s.attachmentMu.TryLock() {
		return nil
	}
	defer s.attachmentMu.Unlock()
	entries, err := os.ReadDir(s.cfg.AttachmentsDir)
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
			files, e := os.ReadDir(filepath.Join(s.cfg.AttachmentsDir, ".tmp"))
			if e != nil {
				return e
			}
			for _, f := range files {
				info, e := f.Info()
				if e == nil && info.ModTime().Before(cutoff) {
					os.Remove(filepath.Join(s.cfg.AttachmentsDir, ".tmp", f.Name()))
				}
			}
			continue
		}
		if !session.IsDir() || !attachment.ValidID(session.Name(), "ss_") {
			continue
		}
		files, e := os.ReadDir(filepath.Join(s.cfg.AttachmentsDir, session.Name()))
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
			exists, e := s.store.AttachmentExists(ctx, session.Name(), f.Name())
			if e != nil {
				return e
			}
			if !exists {
				if e = os.RemoveAll(filepath.Join(s.cfg.AttachmentsDir, session.Name(), f.Name())); e != nil {
					return fmt.Errorf("cleanup: %w", e)
				}
			}
		}
	}
	return nil
}
