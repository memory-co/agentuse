package run

import (
	"archive/zip"
	"bytes"
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
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/internal/protocol"
)

func resourceZIP(t *testing.T, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("SKILL.md")
	fmt.Fprint(f, "---\nname: reviewer\ndescription: Review things\n---\n"+body)
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestNativeInitResourcesAndVersionSessionSelection(t *testing.T) {
	for _, kind := range []string{"codex", "claude"} {
		t.Run(kind, func(t *testing.T) {
			s, home := nativeFixture(t)
			ctx := context.Background()
			conversation, e := s.CreateConversation(ctx, "local", "versions")
			if e != nil {
				t.Fatal(e)
			}
			packagePath := filepath.Join(home, "review.zip")
			if e = os.WriteFile(packagePath, resourceZIP(t, "first skill"), 0600); e != nil {
				t.Fatal(e)
			}
			request := Request{Harness: kind, Prompt: "hello", Model: "test-model", AgentInstructions: "Version one instructions", SkillPackages: []string{packagePath}}
			first, e := s.Start(ctx, "local", conversation.ID, request)
			if e != nil {
				t.Fatal(e)
			}
			first = awaitLocal(t, s, first)
			root := filepath.Join(s.Sessions.Root, "local", conversation.ID, "sessions", first.SessionID)
			resources := filepath.Join(root, "events.jsonl.d", "resources")
			b, e := os.ReadFile(filepath.Join(resources, "agents.md"))
			if e != nil || string(b) != request.AgentInstructions {
				t.Fatal(string(b), e)
			}
			b, e = os.ReadFile(filepath.Join(resources, "skills", "reviewer", "SKILL.md"))
			if e != nil || !strings.Contains(string(b), "first skill") {
				t.Fatal(string(b), e)
			}
			b, e = os.ReadFile(filepath.Join(root, "events.jsonl.d", "config.json"))
			var config struct{ Model string }
			if e != nil || json.Unmarshal(b, &config) != nil || config.Model != "test-model" {
				t.Fatal(string(b), e)
			}
			if _, e = os.Stat(filepath.Join(root, "init-resources")); !os.IsNotExist(e) {
				t.Fatal("staging resources retained", e)
			}
			workspace := filepath.Join(s.Sessions.Root, "local", conversation.ID, "workspace")
			for _, name := range []string{"AGENTS.md", "skills", "review.zip"} {
				if _, e = os.Stat(filepath.Join(workspace, name)); !os.IsNotExist(e) {
					t.Fatal("resources leaked into cwd", name, e)
				}
			}
			second, e := s.Start(ctx, "local", conversation.ID, request)
			if e != nil {
				t.Fatal(e)
			}
			awaitLocal(t, s, second)
			if second.SessionID != first.SessionID {
				t.Fatal("same version did not reuse session")
			}
			request.AgentInstructions = "Version two instructions"
			third, e := s.Start(ctx, "local", conversation.ID, request)
			if e != nil {
				t.Fatal(e)
			}
			awaitLocal(t, s, third)
			if third.SessionID == first.SessionID || third.AgentVersionID == first.AgentVersionID {
				t.Fatal("new configuration reused immutable binding")
			}
			if e = os.WriteFile(packagePath, resourceZIP(t, "second skill"), 0600); e != nil {
				t.Fatal(e)
			}
			fourth, e := s.Start(ctx, "local", conversation.ID, request)
			if e != nil {
				t.Fatal(e)
			}
			awaitLocal(t, s, fourth)
			if fourth.SessionID == third.SessionID {
				t.Fatal("changed skill package reused old binding")
			}
		})
	}
}
func TestRemoteResourceDownloadValidation(t *testing.T) {
	s, _ := localFixture(t)
	data := resourceZIP(t, "remote")
	skill, version := uuid.NewString(), uuid.NewString()
	path := "/api/skills/" + skill + "/versions/" + version + "/package"
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != path || r.Header.Get("Authorization") != "Bearer dk_test" {
			t.Error("invalid download scope")
			w.WriteHeader(403)
			return
		}
		w.Write(data)
	}))
	defer ts.Close()
	ep := endpoint.Endpoint{ServerURL: ts.URL, DeviceID: "device", DeviceKey: "dk_test", Enabled: true}
	if e := s.Endpoints.Save(ep); e != nil {
		t.Fatal(e)
	}
	m := session.Session{EndpointID: ep.ID(), ConversationID: uuid.NewString(), ID: uuid.NewString()}
	digest := sha256.Sum256(data)
	descriptor := protocol.SkillResource{SkillID: skill, SkillVersionID: version, Name: "reviewer", SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), DownloadPath: path}
	a := harness.Adapter{EventsPath: filepath.Join(s.Sessions.Root, m.EndpointID, m.ConversationID, "sessions", m.ID, "events.jsonl")}
	cleanup, e := s.prepareResources(context.Background(), m, Request{skills: []protocol.SkillResource{descriptor}}, &a)
	if e != nil {
		t.Fatal(e)
	}
	cleanup()
	if calls != 1 || len(a.InitArgs) != 2 {
		t.Fatal(calls, a.InitArgs)
	}
	descriptor.SHA256 = strings.Repeat("0", 64)
	_, e = s.prepareResources(context.Background(), m, Request{skills: []protocol.SkillResource{descriptor}}, &a)
	if e == nil {
		t.Fatal("bad checksum accepted")
	}
	descriptor.DownloadPath = "https://other.invalid/package"
	before := calls
	_, e = s.prepareResources(context.Background(), m, Request{skills: []protocol.SkillResource{descriptor}}, &a)
	if e == nil || calls != before {
		t.Fatal("foreign download accepted")
	}
}
