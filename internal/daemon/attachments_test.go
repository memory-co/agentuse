package daemon

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
	"testing"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/protocol"
)

func TestSyncAttachments(t *testing.T) {
	content := "input\x00-data"
	sum := sha256.Sum256([]byte(content))
	a := attachment.Attachment{ID: "at_file", SessionID: "ss_test", Filename: "文档.txt", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(content))}
	downloads := 0
	bad := false
	list := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dk_ok" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/content") {
			downloads++
			if bad {
				w.Write([]byte("corrupted"))
			} else {
				w.Write([]byte(content))
			}
			return
		}
		files := []attachment.Attachment{}
		if list {
			files = append(files, a)
		}
		json.NewEncoder(w).Encode(attachment.Page{Attachments: files})
	}))
	defer ts.Close()
	d, err := New(Config{Server: ts.URL, Key: "dk_ok"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sync := func() (*protocol.Dispatch, error) {
		m := protocol.Dispatch{SessionID: a.SessionID, AttachmentSupport: true}
		err := d.syncAttachments(context.Background(), &m, dir)
		return &m, err
	}
	m, err := sync()
	if err != nil || len(m.Attachments) != 1 {
		t.Fatalf("sync %+v %v", m, err)
	}
	file := filepath.Join(dir, m.Attachments[0].Path)
	b, _ := os.ReadFile(file)
	if string(b) != content {
		t.Fatal("content")
	}
	if _, err = sync(); err != nil || downloads != 1 {
		t.Fatal("cache not reused", downloads, err)
	}
	os.WriteFile(file, []byte("changed"), 0600)
	if _, err = sync(); err != nil || downloads != 2 {
		t.Fatal("corrupt cache", downloads, err)
	}
	bad = true
	os.WriteFile(file, []byte("changed"), 0600)
	if _, err = sync(); err == nil {
		t.Fatal("corrupt download accepted")
	}
	list = false
	m, err = sync()
	if err != nil || len(m.Attachments) != 0 {
		t.Fatal("deleted cache advertised")
	}
	list = true
	os.RemoveAll(filepath.Join(dir, ".agentuse"))
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(dir, ".agentuse"))
	if _, err = sync(); err == nil {
		t.Fatal("symlink cache accepted")
	}
	if files, _ := os.ReadDir(outside); len(files) != 0 {
		t.Fatal("wrote outside workspace")
	}
}
