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

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
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
func acceptedContextEvent(run, input string) map[string]any {
	return map[string]any{"type": "accepted", "run_id": run, "input_id": input, "text": "identical native text"}
}

func TestContextStatusUsesNativeRunWithoutMessageReceipts(t *testing.T) {
	f := newContextFixture(t, "platform")
	f.eventsFile(t, acceptedContextEvent(f.first, "one"), acceptedContextEvent(f.first, "two"), acceptedContextEvent(f.next, "queued"))
	view, err := OpenExecutionContext(context.Background(), f.path, "")
	if err != nil || view.RunID != f.first || view.Status != "running" {
		t.Fatal(view, err)
	}
	data, _ := json.Marshal(view)
	for _, removed := range []string{"inputs", "input_id", "message_id", "text", "attachment_ids"} {
		if strings.Contains(string(data), `"`+removed+`"`) {
			t.Fatal("status exposed input data", string(data))
		}
	}
	next, err := OpenExecutionContext(context.Background(), f.path, f.next)
	if err != nil || next.RunID != f.next {
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

func TestContextHistoricalStatus(t *testing.T) {
	f := newContextFixture(t, endpoint.LocalID)
	f.eventsFile(t, acceptedContextEvent(f.first, "one"), map[string]any{"type": "result", "run_id": f.first, "status": "completed"})
	view, err := OpenExecutionContext(context.Background(), f.path, f.first)
	if err != nil || view.Status != "completed" {
		t.Fatal(view, err)
	}
	files, err := view.Attachments(context.Background())
	if err != nil || len(files) != 0 {
		t.Fatal(files, err)
	}
	if _, err = view.DownloadAttachment(context.Background(), "user_input/missing.png"); err == nil {
		t.Fatal("unknown file accepted")
	}
}

func TestContextAttachmentsComeFromConversationAndDownloadOnDemand(t *testing.T) {
	content := []byte("\x89PNG\r\n\x1a\nimage-data")
	digest := sha256.Sum256(content)
	var image, other sdk.Attachment
	downloads, lists := 0, 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dk_private" {
			t.Error("wrong credential")
			w.WriteHeader(403)
			return
		}
		prefix := "/api/conversations/" + image.ConversationID + "/attachments"
		switch r.URL.Path {
		case prefix:
			lists++
			if r.URL.Query().Get("run_id") != "" {
				t.Error("conversation list filtered by run")
			}
			json.NewEncoder(w).Encode(sdk.AttachmentPage{Attachments: []sdk.Attachment{image, other}})
		case prefix + "/" + image.ID + "/content", prefix + "/" + other.ID + "/content":
			downloads++
			w.Write(content)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	ep := endpoint.Endpoint{ServerURL: ts.URL, DeviceID: "device", DeviceKey: "dk_private", Enabled: true}
	f := newContextFixture(t, ep.ID())
	if err := (&endpoint.Service{Path: f.service.Path}).Save(ep); err != nil {
		t.Fatal(err)
	}
	image = sdk.Attachment{ID: "at_image", ConversationID: f.metadata.ConversationID, Filename: "截图.png", RelativePath: "user_input/截图.png", ContentType: "image/png", SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	other = image
	other.ID = "at_other"
	other.RelativePath = "reports/earlier.png"
	other.CreatedByRunID = f.first
	f.eventsFile(t, acceptedContextEvent(f.first, "one"), acceptedContextEvent(f.next, "next"))
	// A later run sees all conversation files without input receipts.
	view, err := OpenExecutionContext(context.Background(), f.path, f.next)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := view.Attachments(context.Background())
	if err != nil || len(listed) != 2 || listed[1].RunID != f.first || listed[1].Direction != "output" || downloads != 0 {
		t.Fatal(listed, err, downloads)
	}
	if _, err = os.Stat(filepath.Join(f.workspace, "user_input")); !os.IsNotExist(err) {
		t.Fatal("list downloaded files", err)
	}
	if _, err = view.DownloadAttachment(context.Background(), "user_input/missing.png"); err == nil || downloads != 0 {
		t.Fatal("unknown file downloaded", err)
	}
	if _, err = view.DownloadAttachment(context.Background(), image.ID); err == nil || downloads != 0 {
		t.Fatal("file ID accepted as a path", err)
	}
	file, err := view.DownloadAttachment(context.Background(), image.RelativePath)
	if err != nil || file.LocalPath != filepath.Join(f.workspace, filepath.FromSlash(image.RelativePath)) || downloads != 1 {
		t.Fatal(file, err)
	}
	if _, err = view.DownloadAttachment(context.Background(), other.RelativePath); err != nil || downloads != 2 {
		t.Fatal("earlier output inaccessible", err)
	}
	if _, err = view.DownloadAttachment(context.Background(), image.RelativePath); err != nil || downloads != 2 {
		t.Fatal("cache not reused", err)
	}
	os.Remove(file.LocalPath)
	outside := filepath.Join(t.TempDir(), "secret.png")
	os.WriteFile(outside, content, 0600)
	os.Symlink(outside, file.LocalPath)
	if _, err = view.DownloadAttachment(context.Background(), image.RelativePath); err == nil {
		t.Fatal("symbolic link accepted")
	}
	previousLists := lists
	if _, err = view.Attachments(context.Background()); err != nil || lists != previousLists+1 {
		t.Fatal("list did not query Server", err, lists)
	}
}

func TestContextDoesNotExposeCredentialsOrServerBusinessPolicy(t *testing.T) {
	f := newContextFixture(t, "platform")
	f.eventsFile(t, acceptedContextEvent(f.first, "one"))
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

func TestConversationFileContextDoesNotRequireAnActiveRun(t *testing.T) {
	var conversation string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/conversations/"+conversation+"/attachments" || r.Header.Get("X-Agentuse-Run") != "" || r.URL.Query().Get("run_id") != "" {
			t.Error("file listing unexpectedly bound to a run", r.URL)
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(sdk.AttachmentPage{Attachments: []sdk.Attachment{}})
	}))
	defer server.Close()
	ep := endpoint.Endpoint{ServerURL: server.URL, DeviceID: "device", DeviceKey: "dk_private", Enabled: true}
	fixture := newContextFixture(t, ep.ID())
	conversation = fixture.metadata.ConversationID
	if err := (&endpoint.Service{Path: fixture.service.Path}).Save(ep); err != nil {
		t.Fatal(err)
	}
	// No accepted events, run receipts or native process are needed for file access.
	view, err := OpenConversationContext(context.Background(), fixture.path)
	if err != nil || view.RunID != "" {
		t.Fatal(view, err)
	}
	if files, e := view.Attachments(context.Background()); e != nil || len(files) != 0 {
		t.Fatal(files, e)
	}
}
