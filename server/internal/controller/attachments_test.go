package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/server/internal/service"
)

type attachmentFixture struct {
	e            *env
	u            service.User
	token, key   string
	d            service.Device
	conversation service.Conversation
	agent        string
}

func attachmentEnv(t *testing.T, max, total int64) *attachmentFixture {
	t.Setenv("TMPDIR", t.TempDir())
	e := newEnv(t, Config{FilesDir: t.TempDir(), AttachmentMaxBytes: max, AttachmentConversationMaxBytes: total})
	u, tok := e.user("alice", service.RoleUser)
	ctx := context.Background()
	d, err := e.st.CreateDevice(ctx, u.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	d, key, err := e.st.RegisterDevice(ctx, d.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.DeviceConnected(ctx, d, []service.ReportedHarness{{Name: "claude"}}, []string{attachment.Capability, service.RunCapability}); err != nil {
		t.Fatal(err)
	}
	if err = e.st.SetDeviceHarnessEnabled(ctx, u, d.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
	var agent service.Agent
	e.req(tok, "POST", "/api/agents", map[string]string{"name": "coder", "device_id": d.ID, "harness": "claude"}, &agent)
	var res struct {
		Conversation service.Conversation
		Message      *service.Message
	}
	r := e.req(tok, "POST", "/api/conversations", map[string]any{}, &res)
	if r.StatusCode != 201 || res.Conversation.Status != "idle" || res.Message != nil {
		t.Fatalf("deferred: %d %+v", r.StatusCode, res)
	}
	return &attachmentFixture{e, u, tok, key, d, res.Conversation, agent.ID}
}
func (f *attachmentFixture) path() string { return attachment.Path(f.conversation.ID) }
func uploadBody(t *testing.T, filename, data string, fields map[string]string) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	p, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	p.Write([]byte(data))
	w.Close()
	return b.Bytes(), w.FormDataContentType()
}
func rawUpload(t *testing.T, f *attachmentFixture, token, filename, data string, fields map[string]string) (int, attachment.Attachment) {
	t.Helper()
	body, ct := uploadBody(t, filename, data, fields)
	req, _ := http.NewRequest("POST", f.e.ts.URL+f.path(), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var a attachment.Attachment
	json.NewDecoder(r.Body).Decode(&a)
	return r.StatusCode, a
}
func TestAttachmentsLifecycle(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	e := f.e
	if msgs, err := e.st.Messages(context.Background(), f.conversation.ID); err != nil || len(msgs) != 0 {
		t.Fatalf("empty conversation: %v %v", msgs, err)
	}
	var first attachment.Attachment
	for _, body := range []string{"hello", ""} {
		code, a := rawUpload(t, f, f.token, `C:\folder\测试.txt`, body, nil)
		if code != 201 || a.Filename != "测试.txt" || a.Source != "user" || a.SizeBytes != int64(len(body)) {
			t.Fatalf("upload: %d %+v", code, a)
		}
		sum := sha256.Sum256([]byte(body))
		if a.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatal("hash")
		}
		if first.ID == "" {
			first = a
		}
	}
	var page attachment.Page
	e.req(f.token, "GET", f.path()+"?limit=1", nil, &page)
	if len(page.Attachments) != 1 || page.NextCursor == nil {
		t.Fatalf("pagination %+v", page)
	}
	var next attachment.Page
	e.req(f.token, "GET", f.path()+"?limit=1&cursor="+*page.NextCursor, nil, &next)
	if len(next.Attachments) != 1 || next.NextCursor != nil || next.Attachments[0].ID == page.Attachments[0].ID {
		t.Fatalf("next %+v", next)
	}
	if e.code(f.token, "GET", f.path()+"?cursor=broken", nil) != 400 {
		t.Fatal("cursor")
	}
	req, _ := http.NewRequest("GET", e.ts.URL+first.DownloadPath, nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: f.token})
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || string(b) != "hello" || r.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatalf("content %d %s %+v", r.StatusCode, b, r.Header)
	}
	bobUser, bob := e.user("bob", service.RoleUser)
	for _, tok := range []string{bob, ""} {
		want := 404
		if tok == "" {
			want = 401
		}
		if e.code(tok, "GET", first.DownloadPath, nil) != want {
			t.Fatalf("unauthorized %s", tok)
		}
	}
	_, admin := e.user("admin", service.RoleAdmin)
	if e.code(admin, "GET", first.DownloadPath, nil) != 200 {
		t.Fatal("admin")
	}
	if e.req(admin, "GET", first.DownloadPath, nil, nil, actAsHeader, bobUser.ID).StatusCode != 404 {
		t.Fatal("act as scope")
	}
	ss, _, _ := e.st.CreateConversation(context.Background(), f.u, "", "other", "", nil, nil)
	if e.code(f.token, "GET", attachment.Path(ss.ID)+"/"+first.ID+"/content", nil) != 404 {
		t.Fatal("cross-conversation")
	}
	if e.code(f.key, "DELETE", f.path()+"/"+first.ID, nil) != 403 {
		t.Fatal("device delete")
	}
	if e.code(f.token, "DELETE", f.path()+"/"+first.ID, nil) != 204 || e.code(f.token, "GET", first.DownloadPath, nil) != 404 {
		t.Fatal("delete")
	}
	if stream, _, err := e.st.Files.Get(context.Background(), "conversations", first.ConversationID+"/"+first.RelativePath); !os.IsNotExist(err) {
		if stream != nil {
			stream.Close()
		}
		t.Fatal("file not removed", err)
	}
	if e.code(f.token, "DELETE", "/api/conversations/"+f.conversation.ID, nil) != 204 {
		t.Fatal("conversation delete")
	}
	if yes, err := e.st.AttachmentExists(context.Background(), next.Attachments[0].ConversationID, next.Attachments[0].ID); err != nil || yes {
		t.Fatal("cascade")
	}
}
func TestAttachmentConcurrentQuota(t *testing.T) {
	f := attachmentEnv(t, 10, 10)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); code, _ := rawUpload(t, f, f.token, "x", "123456", nil); codes <- code }()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[201] != 1 || counts[413] != 1 {
		t.Fatalf("concurrent quota %v", counts)
	}
}
func TestConversationAttachmentsUseSharedFileStorage(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	code, a := rawUpload(t, f, f.token, "x", "data", nil)
	if code != 201 {
		t.Fatal(code)
	}
	ctx := context.Background()
	stream, _, err := f.e.st.Files.Get(ctx, "conversations", f.conversation.ID+"/"+a.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || string(data) != "data" {
		t.Fatal(string(data), err)
	}
	if err = f.e.st.Files.Put(ctx, "skills", "retained/version/skill.zip", strings.NewReader("keep"), true); err != nil {
		t.Fatal(err)
	}
	if err = f.e.st.Files.Put(ctx, "conversations", f.conversation.ID+"/extra/report.txt", strings.NewReader("raw"), true); err != nil {
		t.Fatal(err)
	}
	if code := f.e.code(f.token, "DELETE", "/api/conversations/"+f.conversation.ID, nil); code != 204 {
		t.Fatal(code)
	}
	files, err := f.e.st.Files.List(ctx, "conversations", f.conversation.ID)
	if err != nil || len(files) != 0 {
		t.Fatal("conversation prefix remains", files, err)
	}
	retained, _, err := f.e.st.Files.Get(ctx, "skills", "retained/version/skill.zip")
	if err != nil {
		t.Fatal("skill package removed with conversation", err)
	}
	retained.Close()
}

func TestAttachmentUploadRechecksIdentityAndConversation(t *testing.T) {
	for _, mode := range []string{"reset-token", "delete-conversation"} {
		t.Run(mode, func(t *testing.T) {
			f := attachmentEnv(t, 1024, 4096)
			pr, pw := io.Pipe()
			mw := multipart.NewWriter(pw)
			req, _ := http.NewRequest("POST", f.e.ts.URL+f.path(), pr)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req.Header.Set("Authorization", "Bearer "+f.token)
			done := make(chan int, 1)
			go func() {
				r, err := http.DefaultClient.Do(req)
				if err != nil {
					done <- 0
					return
				}
				r.Body.Close()
				done <- r.StatusCode
			}()
			part, _ := mw.CreateFormFile("file", "late.txt")
			part.Write([]byte("late"))
			if mode == "reset-token" {
				if _, _, err := f.e.st.ResetUserToken(context.Background(), f.u.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := f.e.st.DeleteConversation(context.Background(), f.u, f.conversation.ID); err != nil {
					t.Fatal(err)
				}
			}
			mw.Close()
			pw.Close()
			code := <-done
			want := 401
			if mode == "delete-conversation" {
				want = 409
			}
			if code != want && !(mode == "delete-conversation" && code == 404) {
				t.Fatalf("got %d want %d", code, want)
			}
			yes, err := f.e.st.AttachmentExists(context.Background(), f.conversation.ID, "at_missing")
			if err != nil || yes {
				t.Fatal("failed upload persisted")
			}
		})
	}
}

func TestSharedAttachmentLimits(t *testing.T) {
	f := attachmentEnv(t, 4, 6)
	if code, _ := rawUpload(t, f, f.token, "big.txt", "12345", nil); code != 413 {
		t.Fatalf("file limit %d", code)
	}
	if code, _ := rawUpload(t, f, f.token, "one.txt", "1234", nil); code != 201 {
		t.Fatalf("upload %d", code)
	}
	if code, _ := rawUpload(t, f, f.token, "two.txt", "123", nil); code != 413 {
		t.Fatalf("conversation quota %d", code)
	}
	if code, _ := rawUpload(t, f, f.token, "obsolete.txt", "x", map[string]string{"message_id": "1"}); code != 400 {
		t.Fatalf("obsolete field %d", code)
	}
}
