package server

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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/store"
)

type attachmentFixture struct {
	e          *env
	u          store.User
	token, key string
	d          store.Daemon
	session    store.Session
	agent      string
}

func attachmentEnv(t *testing.T, max, total int64) *attachmentFixture {
	e := newEnv(t, Config{AttachmentsDir: t.TempDir(), AttachmentMaxBytes: max, AttachmentSessionMaxBytes: total})
	u, tok := e.user("alice", store.RoleUser)
	ctx := context.Background()
	d, err := e.st.CreateDaemon(ctx, u.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	d, key, err := e.st.RegisterDaemon(ctx, d.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.DaemonConnected(ctx, d, []store.ReportedHarness{{Name: "claude"}}, []string{attachment.Capability}); err != nil {
		t.Fatal(err)
	}
	if err = e.st.SetDaemonHarnessEnabled(ctx, u, d.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
	var agent store.Agent
	e.req(tok, "POST", "/api/agents", map[string]string{"name": "coder", "daemon_id": d.ID, "harness": "claude"}, &agent)
	var res struct {
		Session store.Session
		Message *store.Message
	}
	r := e.req(tok, "POST", "/api/sessions", map[string]any{"agent_id": agent.ID, "defer_start": true}, &res)
	if r.StatusCode != 201 || res.Session.Status != "idle" || res.Message != nil {
		t.Fatalf("deferred: %d %+v", r.StatusCode, res)
	}
	return &attachmentFixture{e, u, tok, key, d, res.Session, agent.ID}
}
func (f *attachmentFixture) path() string { return attachment.Path(f.session.ID) }
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
	if msgs, err := e.st.Messages(context.Background(), f.session.ID); err != nil || len(msgs) != 0 {
		t.Fatalf("empty session: %v %v", msgs, err)
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
	bobUser, bob := e.user("bob", store.RoleUser)
	for _, tok := range []string{bob, ""} {
		want := 404
		if tok == "" {
			want = 401
		}
		if e.code(tok, "GET", first.DownloadPath, nil) != want {
			t.Fatalf("unauthorized %s", tok)
		}
	}
	_, admin := e.user("admin", store.RoleAdmin)
	if e.code(admin, "GET", first.DownloadPath, nil) != 200 {
		t.Fatal("admin")
	}
	if e.req(admin, "GET", first.DownloadPath, nil, nil, actAsHeader, bobUser.ID).StatusCode != 404 {
		t.Fatal("act as scope")
	}
	ss, _, _ := e.st.CreateSession(context.Background(), f.u, f.agent, "other", nil)
	if e.code(f.token, "GET", attachment.Path(ss.ID)+"/"+first.ID+"/content", nil) != 404 {
		t.Fatal("cross-session")
	}
	if e.code(f.key, "DELETE", f.path()+"/"+first.ID, nil) != 403 {
		t.Fatal("daemon delete")
	}
	if e.code(f.token, "DELETE", f.path()+"/"+first.ID, nil) != 204 || e.code(f.token, "GET", first.DownloadPath, nil) != 404 {
		t.Fatal("delete")
	}
	if _, err = os.Stat(e.srv.attachmentPath(first.SessionID, first.ID)); !os.IsNotExist(err) {
		t.Fatal("file not removed")
	}
	if e.code(f.token, "DELETE", "/api/sessions/"+f.session.ID, nil) != 204 {
		t.Fatal("session delete")
	}
	if yes, err := e.st.AttachmentExists(context.Background(), next.Attachments[0].SessionID, next.Attachments[0].ID); err != nil || yes {
		t.Fatal("cascade")
	}
}
func TestAttachmentLimitsAndCompatibility(t *testing.T) {
	f := attachmentEnv(t, 5, 7)
	for _, tc := range []struct {
		filename, data string
		fields         map[string]string
		want           int
	}{
		{"x", "123456", nil, 413}, {"x", "123", map[string]string{"filename": "../.."}, 400}, {"x", "1", map[string]string{"filename": "bad\nname"}, 400}, {"x", "1", map[string]string{"message_id": "1"}, 400}, {"x", "1", map[string]string{"unknown": "1"}, 400},
	} {
		if code, _ := rawUpload(t, f, f.token, tc.filename, tc.data, tc.fields); code != tc.want {
			t.Fatalf("%+v: %d", tc, code)
		}
	}
	if c, _ := rawUpload(t, f, f.token, "same", "12345", nil); c != 201 {
		t.Fatal(c)
	}
	if c, _ := rawUpload(t, f, f.token, "same", "123", nil); c != 413 {
		t.Fatal("quota", c)
	}
	if c, _ := rawUpload(t, f, f.token, "same", "12", nil); c != 201 {
		t.Fatal("exact quota", c)
	}
	if _, err := f.e.st.DaemonConnected(context.Background(), f.d, []store.ReportedHarness{{Name: "claude"}}); err != nil {
		t.Fatal(err)
	}
	if c, _ := rawUpload(t, f, f.token, "empty", "", nil); c != 409 {
		t.Fatal("old daemon", c)
	}
	f.e.req(f.token, "POST", "/api/sessions/"+f.session.ID+"/messages", map[string]string{"prompt": "run"}, nil)
	if _, err := f.e.st.ClaimSession(context.Background(), f.d, 60); err != store.ErrNotFound {
		t.Fatal("must not dispatch", err)
	}
	ss, _ := f.e.st.GetSession(context.Background(), f.u, f.session.ID)
	if ss.Status != "idle" || ss.Title != "run" {
		t.Fatalf("failed dispatch %+v", ss)
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
func TestDaemonAttachmentLease(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	if code, _ := rawUpload(t, f, f.key, "x", "a", nil); code != 400 {
		t.Fatal("missing message", code)
	}
	f.e.req(f.token, "POST", "/api/sessions/"+f.session.ID+"/messages", map[string]string{"prompt": "go"}, nil)
	cl, err := f.e.st.ClaimSession(ctx, f.d, 60)
	if err != nil {
		t.Fatal(err)
	}
	if code, a := rawUpload(t, f, f.key, "x", "data", map[string]string{"message_id": strconv.FormatInt(cl.Message.ID, 10)}); code != 201 || a.Source != "daemon" || a.DaemonID != f.d.ID {
		t.Fatalf("daemon upload %d %+v", code, a)
	}
	// Hold the final multipart boundary until the active message has finished.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	req, _ := http.NewRequest("POST", f.e.ts.URL+f.path(), pr)
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
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
	mw.WriteField("message_id", strconv.FormatInt(cl.Message.ID, 10))
	part, _ := mw.CreateFormFile("file", "late")
	part.Write([]byte("late"))
	if err = f.e.st.AddEvent(ctx, f.d.ID, cl.Message.ID, store.EventDone, json.RawMessage(`{"ok":true}`), 60); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	pw.Close()
	if code := <-done; code != 409 {
		t.Fatal("finished during upload", code)
	}
	list, _ := f.e.st.ListAttachments(ctx, f.session.ID, "", 100)
	if len(list.Attachments) != 1 {
		t.Fatal("stale upload committed")
	}
	// Another daemon must not read this session.
	other, err := f.e.st.CreateDaemon(ctx, f.u.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := f.e.st.RegisterDaemon(ctx, other.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.e.code(key, "GET", f.path(), nil) != 404 {
		t.Fatal("cross daemon read")
	}
}
func TestAttachmentOrphanCleanup(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	code, a := rawUpload(t, f, f.token, "x", "data", nil)
	if code != 201 {
		t.Fatal(code)
	}
	orphan := filepath.Join(f.e.srv.cfg.AttachmentsDir, f.session.ID, "at_orphan")
	os.MkdirAll(orphan, 0700)
	os.WriteFile(filepath.Join(orphan, "content"), []byte("x"), 0600)
	old := time.Now().Add(-25 * time.Hour)
	os.Chtimes(orphan, old, old)
	live := filepath.Dir(f.e.srv.attachmentPath(a.SessionID, a.ID))
	os.Chtimes(live, old, old)
	if err := f.e.srv.CleanupAttachments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan remains")
	}
	if _, err := os.Stat(filepath.Join(live, "content")); err != nil {
		t.Fatal("live removed", err)
	}
}

func TestWorkAttachmentScope(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	e := f.e
	work, coord, err := e.st.CreateWork(ctx, f.u, "project", f.agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = e.st.AddMessage(ctx, f.u, coord.ID, json.RawMessage(`{"prompt":"coordinate"}`)); err != nil {
		t.Fatal(err)
	}
	claimed, err := e.st.ClaimSession(ctx, f.d, 60)
	if err != nil || claimed.WorkToken == "" {
		t.Fatalf("claim %v %+v", err, claimed)
	}
	if e.code(claimed.WorkToken, "GET", f.path(), nil) != 403 {
		t.Fatal("work outside scope")
	}
	old := f.session
	f.session = coord
	code, a := rawUpload(t, f, claimed.WorkToken, "coordinator.txt", "in", nil)
	if code != 201 || a.Source != "work" {
		t.Fatalf("coordinator %d %+v", code, a)
	}
	thread, _, err := e.st.CreateSessionIn(ctx, f.u, work.ID, f.agent, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.session = thread
	if code, _ := rawUpload(t, f, claimed.WorkToken, "thread.txt", "data", nil); code != 201 {
		t.Fatal("thread", code)
	}
	if e.code(claimed.WorkToken, "GET", a.DownloadPath, nil) != 200 {
		t.Fatal("read coordinator")
	}
	if e.code(claimed.WorkToken, "DELETE", attachment.Path(coord.ID)+"/"+a.ID, nil) != 204 {
		t.Fatal("delete coordinator")
	}
	if err = e.st.AddEvent(ctx, f.d.ID, claimed.Message.ID, store.EventDone, json.RawMessage(`{"ok":true}`), 60); err != nil {
		t.Fatal(err)
	}
	if e.code(claimed.WorkToken, "GET", f.path(), nil) != 401 {
		t.Fatal("expired work token")
	}
	f.session = old
}

func TestAttachmentUploadRechecksIdentityAndSession(t *testing.T) {
	for _, mode := range []string{"reset-token", "delete-session"} {
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
				if err := f.e.st.DeleteSession(context.Background(), f.u, f.session.ID); err != nil {
					t.Fatal(err)
				}
			}
			mw.Close()
			pw.Close()
			code := <-done
			want := 401
			if mode == "delete-session" {
				want = 409
			}
			if code != want && !(mode == "delete-session" && code == 404) {
				t.Fatalf("got %d want %d", code, want)
			}
			yes, err := f.e.st.AttachmentExists(context.Background(), f.session.ID, "at_missing")
			if err != nil || yes {
				t.Fatal("failed upload persisted")
			}
		})
	}
}
