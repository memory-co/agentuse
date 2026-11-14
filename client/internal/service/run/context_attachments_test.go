package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestContextUploadsOutputsAndRecoversLostResponseFromServer(t *testing.T) {
	var conversation, run string
	outputs := []sdk.Attachment{}
	contents := map[string][]byte{}
	var posts atomic.Int64
	var sourceChecks atomic.Int64
	var mutex sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dk_context" || r.Header.Get("X-Agentuse-Run") != run {
			t.Error("missing bound identity")
			w.WriteHeader(403)
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		path := "/api/conversations/" + conversation + "/attachments"
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/conversations/"+conversation:
			if r.Header.Get("X-Agentuse-Conversation") != conversation {
				t.Error("source check missing conversation identity")
			}
			if sourceChecks.Add(1) <= 2 {
				w.WriteHeader(403)
				json.NewEncoder(w).Encode(map[string]string{"error": "execution source is not yet reported"})
				return
			}
			json.NewEncoder(w).Encode(sdk.ConversationDetail{Conversation: sdk.Conversation{ID: conversation}})
		case r.Method == "GET" && r.URL.Path == path:
			if r.URL.Query().Get("run_id") != "" {
				t.Error("list must not filter by run")
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(sdk.AttachmentPage{Attachments: outputs})
		case r.Method == "POST" && r.URL.Path == path:
			if sourceChecks.Load() < 3 {
				t.Error("upload raced the Server accepted report")
				w.WriteHeader(409)
				return
			}
			number := posts.Add(1)
			file, head, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil {
				t.Error(err)
				return
			}
			digest := sha256.Sum256(data)
			id := "at_report"
			if number == 2 {
				id = "at_lost"
			} else if number == 3 {
				id = "at_renamed"
			}
			metadata := sdk.Attachment{ID: id, ConversationID: conversation, CreatedByRunID: run, Filename: head.Filename, RelativePath: r.FormValue("path"), ContentType: "text/plain", SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
			if number == 2 {
				metadata.RelativePath = "报告 (2).txt"
			}
			if number == 3 {
				metadata.RelativePath = "reports/报告 (3).txt"
			}
			outputs = append(outputs, metadata)
			contents[id] = data
			if number == 2 {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				connection.Close()
				return
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(metadata)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, path+"/") && !strings.HasSuffix(r.URL.Path, "/content"):
			id := strings.TrimPrefix(r.URL.Path, path+"/")
			for _, file := range outputs {
				if file.ID == id {
					json.NewEncoder(w).Encode(file)
					return
				}
			}
			w.WriteHeader(404)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/content"):
			fields := strings.Split(r.URL.Path, "/")
			w.Write(contents[fields[len(fields)-2]])
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	ep := endpoint.Endpoint{ServerURL: server.URL, DeviceID: "device", DeviceKey: "dk_context", Enabled: true}
	fixture := newContextFixture(t, ep.ID())
	conversation, run = fixture.metadata.ConversationID, fixture.first
	if err := (&endpoint.Service{Path: fixture.service.Path}).Save(ep); err != nil {
		t.Fatal(err)
	}
	fixture.eventsFile(t, acceptedContextEvent(run, "one"))
	view, err := OpenExecutionContext(context.Background(), fixture.path, run)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(fixture.workspace, "报告.txt")
	body := []byte("report bytes for the Server user")
	if err = os.WriteFile(report, body, 0600); err != nil {
		t.Fatal(err)
	}
	uploaded, err := view.UploadAttachment(context.Background(), report)
	if err != nil || uploaded.Direction != "output" || uploaded.RunID != run || uploaded.ConversationID != conversation || !strings.HasSuffix(uploaded.DownloadPath, "/at_report/content") {
		t.Fatal(uploaded, err)
	}
	fresh, err := OpenExecutionContext(context.Background(), fixture.path, run)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := fresh.Attachments(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID != uploaded.ID {
		t.Fatal("output required a local receipt", listed, err)
	}
	file, err := fresh.DownloadAttachment(context.Background(), uploaded.RelativePath)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(file.LocalPath)
	if err != nil || string(actual) != string(body) {
		t.Fatal("wrong uploaded output", err)
	}
	if _, err = fresh.UploadAttachment(context.Background(), report); err == nil {
		t.Fatal("interrupted response was reported as success")
	}
	listed, err = fresh.Attachments(context.Background())
	if err != nil || len(listed) != 2 || posts.Load() != 2 {
		t.Fatal("lost upload response was retried or hidden", listed, posts.Load(), err)
	}
	if _, err = fresh.UploadAttachment(context.Background(), fixture.workspace); err == nil || posts.Load() != 2 {
		t.Fatal("directory was uploaded", err)
	}
	renamed, err := fresh.UploadAttachment(context.Background(), report)
	if err != nil || renamed.RelativePath != "reports/报告 (3).txt" || renamed.LocalPath != filepath.Join(fixture.workspace, "reports", "报告 (3).txt") {
		t.Fatal("Server-assigned path not mirrored", renamed, err)
	}
	if data, e := os.ReadFile(renamed.LocalPath); e != nil || string(data) != string(body) {
		t.Fatal("renamed upload not materialized", e)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, body, 0600)
	if _, err = fresh.UploadAttachment(context.Background(), outside); err == nil || posts.Load() != 3 {
		t.Fatal("uploaded outside workspace", err)
	}
	fixture.eventsFile(t, acceptedContextEvent(run, "one"), map[string]any{"type": "result", "run_id": run, "status": "completed"})
	historical, err := OpenExecutionContext(context.Background(), fixture.path, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = historical.UploadAttachment(context.Background(), report); err == nil || posts.Load() != 3 {
		t.Fatal("historical context uploaded", err)
	}
}
