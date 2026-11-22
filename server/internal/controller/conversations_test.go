package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestConversationsReplaceServerSessionRoutesAndFields(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, token := e.user("conversation-owner", sdk.RoleUser)
	var created map[string]json.RawMessage
	if code := e.req(token, "POST", "/api/conversations", map[string]any{}, &created).StatusCode; code != http.StatusCreated {
		t.Fatal(code)
	}
	if len(created["conversation"]) == 0 || created["session"] != nil {
		t.Fatal("incorrect create response", created)
	}
	var conversation sdk.Conversation
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
	for _, name := range []string{"Message", "Attachment", "Run", "Question", "ConversationEvent"} {
		properties := spec.Components.Schemas[name]["properties"].(map[string]any)
		if properties["conversation_id"] == nil || properties["client_session_id"] != nil {
			t.Fatal("old ownership field in schema", name, properties)
		}
	}
	for _, name := range []string{"Run", "ConversationEvent"} {
		if spec.Components.Schemas[name]["properties"].(map[string]any)["session_id"] == nil {
			t.Fatal("Client native session identity was lost", name)
		}
	}
	input := spec.Components.Schemas["RunInput"]["properties"].(map[string]any)
	if len(input) != 4 || input["run_id"] == nil || input["input_id"] == nil || input["message_id"] == nil || input["created_at"] == nil {
		t.Fatal("accepted inputs must contain facts only", input)
	}
	if spec.Components.Schemas["Delivery"] != nil {
		t.Fatal("removed delivery model is documented")
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

func TestRenameConversation(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, owner := e.user("owner", sdk.RoleUser)
	_, other := e.user("other", sdk.RoleUser)
	var created sdk.CreateConversationResponse
	if code := e.req(owner, "POST", "/api/conversations", map[string]string{"prompt": "Initial message"}, &created).StatusCode; code != http.StatusCreated {
		t.Fatal(code)
	}
	path := "/api/conversations/" + created.Conversation.ID
	body := map[string]string{"title": "  網誌配圖調整  "}
	if code := e.code(other, "PATCH", path, body); code != http.StatusNotFound {
		t.Fatalf("unrelated user got status %d", code)
	}
	if code := e.code(owner, "PATCH", path, map[string]string{"title": "   "}); code != http.StatusBadRequest {
		t.Fatalf("blank name got status %d", code)
	}
	var renamed sdk.Conversation
	if code := e.req(owner, "PATCH", path, body, &renamed).StatusCode; code != http.StatusOK ||
		renamed.Title != "網誌配圖調整" || renamed.ID != created.Conversation.ID || renamed.Status != created.Conversation.Status {
		t.Fatal(code, renamed)
	}
	if code := e.code(owner, "POST", path+"/messages", map[string]string{"prompt": "Follow-up"}); code != http.StatusCreated {
		t.Fatal(code)
	}
	var detail sdk.ConversationDetail
	if code := e.req(owner, "GET", path, nil, &detail).StatusCode; code != http.StatusOK ||
		detail.Conversation.Title != renamed.Title || len(detail.Messages) != 2 || detail.Messages[0].Content != "Initial message" {
		t.Fatal(code, detail)
	}
}

func TestDeviceRoutesUseOnlyV3ResourceNames(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, token := e.user("device-owner", sdk.RoleUser)
	var device sdk.Device
	if code := e.req(token, "POST", "/api/devices", map[string]string{"name": "Worker"}, &device).StatusCode; code != 201 {
		t.Fatal(code)
	}
	if code := e.code("", "POST", "/api/client/register", map[string]string{"token": device.SetupToken}); code != 426 {
		t.Fatal("legacy plaintext exchange enabled", code)
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
