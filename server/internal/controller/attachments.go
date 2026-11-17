package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/attachment"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	conversationservice "github.com/memory-co/agentuse/server/internal/service/conversation"
)

type attachmentKey struct{}

func attachmentAccess(r *http.Request) conversationservice.AttachmentAccess {
	return r.Context().Value(attachmentKey{}).(conversationservice.AttachmentAccess)
}
func (s *Server) attachmentAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := requestToken(r)
		if !resourceCredential(r) {
			s.auth(func(w http.ResponseWriter, r *http.Request) {
				a := conversationservice.AttachmentAccess{User: currentUser(r), Token: token}
				h(w, r.WithContext(context.WithValue(r.Context(), attachmentKey{}, a)))
			})(w, r)
			return
		}
		var err error
		r, err = s.executionRequest(r)
		if err != nil {
			writeErr(w, 403, "execution access unavailable")
			return
		}
		p := currentExecution(r)
		if p.Preparing || r.Method == http.MethodDelete {
			writeErr(w, 403, "run credential required")
			return
		}
		a := conversationservice.AttachmentAccess{User: currentUser(r), Token: token, DeviceID: p.DeviceID, RunID: p.RunID, SourceConversationID: p.Claims.ConversationID, SourceAgentID: p.Claims.AgentID}
		ctx, cancel := context.WithDeadline(r.Context(), time.Unix(p.ExpiresAt, 0))
		defer cancel()
		h(w, r.WithContext(context.WithValue(ctx, attachmentKey{}, a)))
	}
}
func (s *Server) attachmentErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, conversationservice.ErrAttachmentUnauthorized):
		writeErr(w, 401, err.Error())
	case errors.Is(err, database.ErrNotFound):
		writeErr(w, 404, "会话或附件不存在")
	case errors.Is(err, conversationservice.ErrAttachmentReferenced), errors.Is(err, conversationservice.ErrAttachmentPathConflict), errors.Is(err, conversationservice.ErrAttachmentRun), errors.Is(err, conversationservice.ErrAttachmentCapability):
		writeErr(w, 409, err.Error())
	case errors.Is(err, conversationservice.ErrAttachmentQuota):
		writeErr(w, 413, err.Error())
	default:
		s.log.Printf("attachment: %v", err)
		writeErr(w, 500, "附件存储失败")
	}
}
func (s *Server) attachmentConversation(w http.ResponseWriter, r *http.Request, upload bool) bool {
	id := r.PathValue("id")
	if uuid.Validate(id) != nil {
		writeErr(w, 404, "会话不存在")
		return false
	}
	a := attachmentAccess(r)
	if _, err := s.service.ConversationService.AttachmentConversation(r.Context(), a, id, upload); err != nil {
		s.attachmentErr(w, err)
		return false
	}
	return true
}
func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	// Authenticate the conversation before parsing potentially large request bodies.
	if !s.attachmentConversation(w, r, true) {
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
	f, err := os.CreateTemp("", "agentuse-upload-*")
	if err != nil {
		s.attachmentErr(w, err)
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	var filename, override, requestedPath, ctype, digest string
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
		if seen[name] || (name != "file" && name != "filename" && name != "path") {
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
			if name == "path" {
				requestedPath = string(b)
			} else {
				override = string(b)
			}
		}
		part.Close()
	}
	if !seen["file"] {
		writeErr(w, 400, "缺少 file")
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
	if requestedPath == "" {
		requestedPath = filename
	}
	requestedPath, err = attachment.RelativePath(requestedPath)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if a.DeviceID == "" {
		if !strings.HasPrefix(requestedPath, "user_input/") {
			requestedPath = "user_input/" + requestedPath
		}
	} else if requestedPath == "user_input" || strings.HasPrefix(requestedPath, "user_input/") {
		writeErr(w, 400, "user_input/ 专用于用户上传")
		return
	}
	requestedPath, err = attachment.RelativePath(requestedPath)
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
	v := attachment.Attachment{RelativePath: requestedPath, ID: s.service.ConversationService.NewAttachmentID(), ConversationID: r.PathValue("id"), Filename: filename, ContentType: ctype, SizeBytes: size, SHA256: digest, CreatedBy: a.User.ID, Source: "user", CreatedAt: time.Now().Unix()}
	if a.DeviceID != "" {
		v.Source = "device"
		v.DeviceID = a.DeviceID
		v.CreatedByRunID = a.RunID
	}
	if a.RunID != "" {
		v.CreatedByRunID = a.RunID
		if run, e := s.service.RunService.GetRun(r.Context(), agentuse.User{Role: agentuse.RoleAdmin}, v.ConversationID, a.RunID); e == nil {
			v.CreatedByAgentID = run.AgentID
		}
	}
	err = s.service.ConversationService.PublishAttachment(r.Context(), a, &v, tmp)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeErr(w, 409, "上传期间会话或权限发生变化")
			return
		}
		s.attachmentErr(w, err)
		return
	}
	v.DownloadPath = "/api/conversations/" + v.ConversationID + "/attachments/" + v.ID + "/content"
	w.Header().Set("Location", strings.TrimSuffix(v.DownloadPath, "/content"))
	writeJSON(w, 201, v)
}
func (s *Server) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentConversation(w, r, false) {
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
	run := r.URL.Query().Get("run_id")
	if run != "" {
		if uuid.Validate(run) != nil {
			writeErr(w, 400, "run_id 必须是 UUID")
			return
		}
		if err := s.service.ConversationService.AttachmentRun(r.Context(), attachmentAccess(r), r.PathValue("id"), run); err != nil {
			s.attachmentErr(w, err)
			return
		}
	}
	page, err := s.service.ConversationService.ListAttachments(r.Context(), r.PathValue("id"), r.URL.Query().Get("cursor"), limit, run)
	if err != nil {
		if errors.Is(err, conversationservice.ErrAttachmentCursor) {
			writeErr(w, 400, err.Error())
		} else {
			s.attachmentErr(w, err)
		}
		return
	}
	writeJSON(w, 200, page)
}
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentConversation(w, r, false) {
		return
	}
	v, err := s.service.ConversationService.GetAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachment_id"))
	if err != nil {
		s.attachmentErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleAttachmentContent(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentConversation(w, r, false) {
		return
	}
	v, err := s.service.ConversationService.GetAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachment_id"))
	if err != nil {
		s.attachmentErr(w, err)
		return
	}
	f, err := s.service.ConversationService.OpenAttachmentFile(r.Context(), v.ConversationID, v.ID)
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
	if _, err = s.copyResource(w, r, f); err != nil {
		s.log.Printf("attachment download: %v", err)
	}
}
func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.attachmentConversation(w, r, false) {
		return
	}
	if err := s.service.ConversationService.DeleteAttachment(r.Context(), attachmentAccess(r), r.PathValue("id"), r.PathValue("attachment_id")); err != nil {
		s.attachmentErr(w, err)
		return
	}
	w.WriteHeader(204)
}
