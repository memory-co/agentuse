package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestConversationsReplaceServerSessionRoutesAndFields(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, token := e.user("conversation-owner", service.RoleUser)
	var created map[string]json.RawMessage
	if code := e.req(token, "POST", "/api/conversations", map[string]any{}, &created).StatusCode; code != http.StatusCreated {
		t.Fatal(code)
	}
	if len(created["conversation"]) == 0 || created["session"] != nil {
		t.Fatal("incorrect create response", created)
	}
	var conversation service.Conversation
	if err := json.Unmarshal(created["conversation"], &conversation); err != nil || conversation.ID == "" {
		t.Fatal(conversation, err)
	}
	var reply struct {
		Message map[string]json.RawMessage `json:"message"`
	}
	path := "/api/conversations/" + conversation.ID
	if code := e.req(token, "POST", path+"/messages", map[string]any{"prompt": "a conversation note"}, &reply).StatusCode; code != http.StatusCreated {
		t.Fatal(code)
	}
	var id string
	json.Unmarshal(reply.Message["conversation_id"], &id)
	if id != conversation.ID || reply.Message["session_id"] != nil {
		t.Fatal("incorrect message ownership", reply.Message)
	}
	var authorRole, content string
	json.Unmarshal(reply.Message["author_role"], &authorRole)
	json.Unmarshal(reply.Message["content"], &content)
	if authorRole != "user" || content != "a conversation note" || reply.Message["role"] != nil || reply.Message["run_id"] != nil || reply.Message["agent_key"] != nil {
		t.Fatal("obsolete message model", reply.Message)
	}
	for _, legacy := range []map[string]any{{"mode": "multi_agent"}, {"defer_start": true}, {"input": map[string]string{"prompt": "legacy"}}} {
		if code := e.code(token, "POST", "/api/conversations", legacy); code != http.StatusBadRequest {
			t.Fatal("legacy request field accepted", legacy, code)
		}
	}
	var detail map[string]json.RawMessage
	if code := e.req(token, "GET", path, nil, &detail).StatusCode; code != http.StatusOK || len(detail["conversation"]) == 0 || detail["session"] != nil {
		t.Fatal(code, detail)
	}
	listed, err := sdk.New(e.ts.URL, token).ListConversations(context.Background(), "")
	if err != nil || len(listed) != 1 || listed[0].ID != conversation.ID {
		t.Fatal("SDK conversation listing differs", listed, err)
	}

	spec := readServerAPISpec(t, e, token)
	for _, name := range []string{"Message", "Attachment", "Run", "Delivery", "Question", "ConversationEvent"} {
		properties := spec.Components.Schemas[name]["properties"].(map[string]any)
		if properties["conversation_id"] == nil || properties["client_session_id"] != nil {
			t.Fatal("old ownership field in schema", name, properties)
		}
	}
	for _, name := range []string{"Run", "Delivery", "ConversationEvent"} {
		if spec.Components.Schemas[name]["properties"].(map[string]any)["session_id"] == nil {
			t.Fatal("Client native session identity was lost", name)
		}
	}
	parameters := regexp.MustCompile(`\{[^}]+\}`)
	for path, methods := range spec.Paths {
		if strings.HasPrefix(path, "/api/session") {
			t.Fatal("old Server route is documented", path)
		}
		if !strings.HasPrefix(path, "/api/conversations") {
			continue
		}
		for _, prefix := range []string{"/api/sessions", "/api/session"} {
			old := parameters.ReplaceAllString(strings.Replace(path, "/api/conversations", prefix, 1), "example")
			for method := range methods {
				if code := e.code(token, strings.ToUpper(method), old, nil); code != http.StatusNotFound {
					t.Fatalf("old route responds to %s %s: %d", method, old, code)
				}
			}
		}
	}
}

func TestDeviceRoutesUseOnlyV3ResourceNames(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, token := e.user("device-owner", service.RoleUser)
	var device service.Device
	if code := e.req(token, "POST", "/api/devices", map[string]string{"name": "Worker"}, &device).StatusCode; code != 201 {
		t.Fatal(code)
	}
	var registered map[string]json.RawMessage
	if code := e.req("", "POST", "/api/client/register", map[string]string{"token": device.SetupToken, "hostname": "machine"}, &registered).StatusCode; code != 200 {
		t.Fatal(code)
	}
	if registered["device_id"] == nil || registered["daemon_id"] != nil {
		t.Fatal("obsolete registration identity", registered)
	}
	for path, method := range map[string]string{"/api/daemons": "GET", "/api/daemons/" + device.ID: "DELETE", "/api/daemon/register": "POST", "/api/daemon/me": "GET"} {
		if code := e.code(token, method, path, nil); code != 404 {
			t.Fatal("legacy device route exists", method, path, code)
		}
	}
	spec := readServerAPISpec(t, e, token)
	for path := range spec.Paths {
		if strings.HasPrefix(path, "/api/daemon") {
			t.Fatal("legacy device path documented", path)
		}
	}
}
