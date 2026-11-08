package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestContextDownloadResolvesConversationPathsAcrossPagesWithoutRun(t *testing.T) {
	content := []byte("the selected report")
	digest := sha256.Sum256(content)
	var selected, sameName sdk.Attachment
	var requests, downloads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer dk_download" || r.Header.Get("X-Agentuse-Run") != "" || r.URL.Query().Get("run_id") != "" {
			t.Error("download unexpectedly required a run or used the wrong identity")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		prefix := "/api/conversations/" + selected.ConversationID + "/attachments"
		switch {
		case r.URL.Path == prefix && r.URL.Query().Get("cursor") == "":
			next := "second-page"
			json.NewEncoder(w).Encode(sdk.AttachmentPage{Attachments: []sdk.Attachment{sameName}, NextCursor: &next})
		case r.URL.Path == prefix && r.URL.Query().Get("cursor") == "second-page":
			json.NewEncoder(w).Encode(sdk.AttachmentPage{Attachments: []sdk.Attachment{selected}})
		case r.URL.Path == prefix+"/"+selected.ID+"/content":
			downloads.Add(1)
			w.Write(content)
		default:
			t.Errorf("unexpected attachment request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ep := endpoint.Endpoint{ServerURL: server.URL, DeviceID: "device", DeviceKey: "dk_download", Enabled: true}
	fixture := newContextFixture(t, ep.ID())
	if err := (&endpoint.Service{Path: fixture.service.Path}).Save(ep); err != nil {
		t.Fatal(err)
	}
	selected = sdk.Attachment{ID: "at_report", ConversationID: fixture.metadata.ConversationID, Filename: "notes 1.txt", RelativePath: "reports/每日/notes 1.txt", ContentType: "text/plain", SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	sameName = selected
	sameName.ID = "at_input"
	sameName.RelativePath = "user_input/notes 1.txt"
	// No accepted events or run receipts exist for this context.
	view, err := OpenConversationContext(context.Background(), fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := view.DownloadAttachment(context.Background(), selected.RelativePath)
	if err != nil || file.ID != selected.ID || file.LocalPath != filepath.Join(fixture.workspace, filepath.FromSlash(selected.RelativePath)) {
		t.Fatal("wrong path selected", file, err)
	}
	if data, err := os.ReadFile(file.LocalPath); err != nil || string(data) != string(content) {
		t.Fatal("downloaded content differs", err)
	}
	if downloads.Load() != 1 {
		t.Fatal("downloaded files other than the selected path")
	}
	before := requests.Load()
	for _, path := range []string{"", ".", "../escape.txt", "/absolute.txt", "reports/../escape.txt", `reports\escape.txt`} {
		if _, err := view.DownloadAttachment(context.Background(), path); err == nil {
			t.Fatal("invalid download path accepted", path)
		}
	}
	if requests.Load() != before {
		t.Fatal("invalid paths reached the Server")
	}
	for _, missing := range []string{selected.ID, selected.Filename, "missing/notes.txt"} {
		if _, err := view.DownloadAttachment(context.Background(), missing); err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatal("download did not require the exact conversation path", missing, err)
		}
	}
	if downloads.Load() != 1 {
		t.Fatal("missing paths downloaded content")
	}
}
