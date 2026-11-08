package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/internal/protocol"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

type contextFixture struct {
	service                              *Service
	metadata                             session.Session
	adapter                              harness.Adapter
	path, events, workspace, first, next string
}

func newContextFixture(t *testing.T, ep string) contextFixture {
	t.Helper()
	root := t.TempDir()
	sessions, err := session.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m := session.Session{ID: uuid.NewString(), EndpointID: ep, ConversationID: uuid.NewString(), AgentID: "reviewer", AgentVersionID: "version", Harness: "test", HarnessRevision: "revision", Status: "running"}
	if err = sessions.SaveSession(m); err != nil {
		t.Fatal(err)
	}
	workspace := sessionWorkspace(root, ep, m.ConversationID)
	if err = os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	first, next := uuid.NewString(), uuid.NewString()
	status := fmt.Sprintf(`{"state":"running","run_id":%q,"queued":1,"requests":[]}`, first)
	adapter := harness.Adapter{Path: fakeBin(t, t.TempDir(), "harnessctl", fmt.Sprintf("printf '%%s\\n' '%s'\n", status))}
	svc := &Service{Sessions: sessions, Path: filepath.Join(root, "endpoints.yaml")}
	if err = svc.prepareContext(m, &adapter); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ep, m.ConversationID, "sessions", m.ID, "context.json")
	return contextFixture{svc, m, adapter, path, filepath.Join(filepath.Dir(path), "events.jsonl"), workspace, first, next}
}
func (f contextFixture) eventsFile(t *testing.T, events ...map[string]any) {
	t.Helper()
	var data []byte
	for _, event := range events {
		b, _ := json.Marshal(event)
		data = append(data, append(b, '\n')...)
	}
	if err := fileutil.Atomic(f.events, data); err != nil {
		t.Fatal(err)
	}
}
func (f contextFixture) receipt(t *testing.T, run, input, message, text string, files ...sdk.Attachment) messageReceipt {
	t.Helper()
	p := protocol.ConversationMessage{ConversationID: f.metadata.ConversationID, MessageID: message, AgentID: f.metadata.AgentID, AgentVersionID: f.metadata.AgentVersionID, Content: text, Context: "shared history", Attachments: []protocol.LocalAttachment{}}
	for _, a := range files {
		p.Attachments = append(p.Attachments, protocol.LocalAttachment{Attachment: a})
	}
	r := messageReceipt{Message: p, SessionID: f.metadata.ID, RunID: run, InputID: input, State: "accepted"}
	if err := fileutil.WriteJSON(filepath.Join(f.service.Sessions.Root, f.metadata.EndpointID, f.metadata.ConversationID, ".server", "messages", receiptName(p.Key())), r); err != nil {
		t.Fatal(err)
	}
	return r
}
func acceptedContextEvent(run, input string) map[string]any {
	return map[string]any{"type": "accepted", "run_id": run, "input_id": input, "text": "identical native text"}
}

func TestContextUsesNativeActiveRunAndAcceptedInputMembership(t *testing.T) {
	f := newContextFixture(t, "platform")
	f.eventsFile(t, acceptedContextEvent(f.first, "one"), acceptedContextEvent(f.first, "two"), acceptedContextEvent(f.next, "queued"))
	f.receipt(t, f.first, "one", "message-one", "first")
	f.receipt(t, f.first, "two", "message-two", "supplement")
	f.receipt(t, f.next, "queued", "message-next", "future")
	view, err := OpenExecutionContext(context.Background(), f.path, "")
	if err != nil || view.RunID != f.first || len(view.Inputs) != 2 || view.Inputs[0].Text != "first" || view.Inputs[1].Text != "supplement" {
		t.Fatal(view, err)
	}
	next, err := OpenExecutionContext(context.Background(), f.path, f.next)
	if err != nil || len(next.Inputs) != 1 || next.Inputs[0].Text != "future" {
		t.Fatal(next, err)
	}
	if _, err = OpenExecutionContext(context.Background(), f.path, uuid.NewString()); err == nil {
		t.Fatal("unrelated run accepted")
	}
	if _, err = OpenExecutionContext(context.Background(), f.path, "not-a-run"); err == nil {
		t.Fatal("invalid run accepted")
	}
	if _, err = OpenExecutionContext(context.Background(), "", ""); err == nil {
		t.Fatal("unbound context silently selected")
	}
}

func TestContextWaitsForExactAcceptedReceipt(t *testing.T) {
	f := newContextFixture(t, "platform")
	f.eventsFile(t, acceptedContextEvent(f.first, "one"))
	r := f.receipt(t, f.first, "one", "message-one", "hello")
	path := filepath.Join(f.service.Sessions.Root, f.metadata.EndpointID, f.metadata.ConversationID, ".server", "messages", receiptName(r.Message.Key()))
	pending := r
	pending.State = "submitting"
	pending.RunID = ""
	pending.InputID = ""
	if err := fileutil.WriteJSON(path, pending); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { time.Sleep(40 * time.Millisecond); done <- fileutil.WriteJSON(path, r) }()
	view, err := OpenExecutionContext(context.Background(), f.path, f.first)
	if err != nil || len(view.Inputs) != 1 || view.Inputs[0].MessageID != "message-one" {
		t.Fatal(view, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestContextLocalInputsAndHistoricalSelection(t *testing.T) {
	f := newContextFixture(t, endpoint.LocalID)
	f.eventsFile(t, acceptedContextEvent(f.first, "one"), map[string]any{"type": "result", "run_id": f.first, "status": "completed"})
	view, err := OpenExecutionContext(context.Background(), f.path, f.first)
	if err != nil || view.Status != "completed" || len(view.Inputs) != 1 || view.Inputs[0].Text != "identical native text" || view.Inputs[0].MessageID != "" {
		t.Fatal(view, err)
	}
	files, err := view.Attachments(context.Background())
	if err != nil || len(files) != 0 {
		t.Fatal(files, err)
	}
	if _, err = view.Attachment(context.Background(), "at_missing"); err == nil {
		t.Fatal("unknown file accepted")
	}
}

func TestContextAttachmentSnapshotVerificationAndScopedDownload(t *testing.T) {
	content := []byte("\x89PNG\r\n\x1a\nimage-data")
	digest := sha256.Sum256(content)
	requests := 0
	var allowed, listPath, runID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if r.URL.Path == listPath {
			if r.URL.Query().Get("run_id") != runID || r.Header.Get("Authorization") != "Bearer dk_private" {
				t.Error("unscoped list")
			}
			json.NewEncoder(w).Encode(sdk.AttachmentPage{Attachments: []sdk.Attachment{}})
			return
		}
		requests++
		if r.Header.Get("Authorization") != "Bearer dk_private" || r.URL.Path != allowed {
			t.Errorf("unscoped request %s", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		w.Write(content)
	}))
	defer ts.Close()
	ep := endpoint.Endpoint{ServerURL: ts.URL, DeviceID: "device", DeviceKey: "dk_private", Enabled: true}
	f := newContextFixture(t, ep.ID())
	if err := (&endpoint.Service{Path: f.service.Path}).Save(ep); err != nil {
		t.Fatal(err)
	}
	image := sdk.Attachment{ID: "at_image", SessionID: f.metadata.ConversationID, Filename: "截图.png", ContentType: "image/png", SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	future := image
	future.ID = "at_future"
	allowed = "/api/sessions/" + f.metadata.ConversationID + "/attachments/at_image/content"
	listPath = "/api/sessions/" + f.metadata.ConversationID + "/attachments"
	runID = f.first
	f.eventsFile(t, acceptedContextEvent(f.first, "one"), acceptedContextEvent(f.next, "next"))
	f.receipt(t, f.first, "one", "message", "inspect image", image)
	f.receipt(t, f.next, "next", "later", "other image", future)
	view, err := OpenExecutionContext(context.Background(), f.path, f.first)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := view.Attachments(context.Background())
	if err != nil || len(listed) != 1 || listed[0].MIMEType != "image/png" {
		t.Fatal(listed, err)
	}
	if _, err = view.Attachment(context.Background(), future.ID); err == nil || requests != 0 {
		t.Fatal("fetched a future input file", err, requests)
	}
	file, err := view.Attachment(context.Background(), image.ID)
	if err != nil || !filepath.IsAbs(file.LocalPath) || requests != 1 {
		t.Fatal(file, err, requests)
	}
	actual, err := os.ReadFile(file.LocalPath)
	if err != nil || string(actual) != string(content) {
		t.Fatal("wrong image", err)
	}
	if _, err = view.Attachment(context.Background(), image.ID); err != nil || requests != 1 {
		t.Fatal("verified cache not reused", err, requests)
	}
	if err = os.WriteFile(file.LocalPath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = view.Attachment(context.Background(), image.ID); err != nil || requests != 2 {
		t.Fatal("corrupt image not repaired", err, requests)
	}
	if err = os.Remove(file.LocalPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.png")
	os.WriteFile(outside, content, 0600)
	if err = os.Symlink(outside, file.LocalPath); err != nil {
		t.Fatal(err)
	}
	if _, err = view.Attachment(context.Background(), image.ID); err == nil {
		t.Fatal("symbolic link exposed as an image")
	}
}

func TestContextDoesNotExposeCredentialsOrServerBusinessPolicy(t *testing.T) {
	f := newContextFixture(t, "platform")
	f.eventsFile(t, acceptedContextEvent(f.first, "one"))
	f.receipt(t, f.first, "one", "message", "input")
	view, err := OpenExecutionContext(context.Background(), f.path, f.first)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(view)
	for _, private := range []string{"work_token", "work_id", "config_path", "adapter_path", "device_key"} {
		if strings.Contains(string(data), private) {
			t.Fatal("private field exposed", private)
		}
	}
	if !strings.Contains(strings.Join(f.adapter.Env, "\n"), "AGENTUSE_SESSION="+f.metadata.ID) {
		t.Fatal("session id was not injected")
	}
	if info, err := os.Lstat(filepath.Join(view.BinPath(), "context")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("missing context entrypoint", err)
	}
}
