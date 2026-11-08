package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestMaterializeAttachmentUsesConversationPathAndVerifiesBytes(t *testing.T) {
	content := []byte("input\x00-data")
	sum := sha256.Sum256(content)
	a := sdk.Attachment{ID: "at_file", SessionID: "conversation", Filename: "文档.txt", RelativePath: "user_input/文档.txt", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(content))}
	downloads := 0
	bad := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dk_ok" || r.URL.Path != "/api/sessions/conversation/attachments/at_file/content" {
			t.Error("wrong download scope")
			w.WriteHeader(403)
			return
		}
		downloads++
		if bad {
			w.Write([]byte("corrupted"))
		} else {
			w.Write(content)
		}
	}))
	defer server.Close()
	client := sdk.New(server.URL, "dk_ok")
	dir := t.TempDir()
	materialize := func() (string, error) {
		return materializeAttachment(context.Background(), client, a.SessionID, dir, a, nil)
	}
	path, err := materialize()
	if err != nil || path != filepath.Join(dir, filepath.FromSlash(a.RelativePath)) {
		t.Fatal(path, err)
	}
	if b, e := os.ReadFile(path); e != nil || string(b) != string(content) {
		t.Fatal("wrong file", e)
	}
	if _, err = materialize(); err != nil || downloads != 1 {
		t.Fatal("verified bytes not reused", downloads, err)
	}
	os.WriteFile(path, []byte("changed"), 0600)
	if _, err = materialize(); err != nil || downloads != 2 {
		t.Fatal("corrupt download not repaired", downloads, err)
	}
	bad = true
	os.WriteFile(path, []byte("changed"), 0600)
	if _, err = materialize(); err == nil {
		t.Fatal("corrupt download accepted")
	}
	if b, _ := os.ReadFile(path); string(b) != "changed" {
		t.Fatal("unverified bytes replaced file")
	}
	for _, relative := range []string{"../escape", "/absolute", "a/../../escape", `a\escape`, ""} {
		invalid := a
		invalid.RelativePath = relative
		if _, err = materializeAttachment(context.Background(), client, a.SessionID, dir, invalid, nil); err == nil {
			t.Fatal("unsafe path accepted", relative)
		}
	}
	os.RemoveAll(filepath.Join(dir, "user_input"))
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(dir, "user_input"))
	if _, err = materialize(); err == nil {
		t.Fatal("symbolic-link parent accepted")
	}
	if files, _ := os.ReadDir(outside); len(files) != 0 {
		t.Fatal("wrote outside workspace")
	}
}
