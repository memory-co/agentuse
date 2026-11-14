package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestNestedConversationAPISourceScopesAndIndependentFiles(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	ctx := context.Background()
	u, token := e.user("nested-owner", service.RoleUser)
	human := sdk.New(e.ts.URL, token)
	device := func(name string) (service.Device, *sdk.Client) {
		d, err := e.st.CreateDevice(ctx, u.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		d, key, err := e.st.RegisterDevice(ctx, d.SetupToken, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.st.DeviceConnected(ctx, d, []service.ReportedHarness{{Name: "codex"}}); err != nil {
			t.Fatal(err)
		}
		if err = e.st.SetDeviceHarnessEnabled(ctx, u, d.ID, "codex", true); err != nil {
			t.Fatal(err)
		}
		return d, sdk.New(e.ts.URL, key)
	}
	d, parentClient := device("coordinator")
	other, childClient := device("executor")
	a, err := human.CreateAgent(ctx, "", sdk.CreateAgentRequest{Name: "Coordinator", DeviceID: d.ID, Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := human.CreateAgent(ctx, "", sdk.CreateAgentRequest{Name: "Executor", DeviceID: other.ID, Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := human.CreateConversation(ctx, "", sdk.CreateConversationRequest{Mode: "collaborative", Prompt: "coordinate", ToAgentIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parentClient.CreateConversation(ctx, "", sdk.CreateConversationRequest{ParentConversationID: parent.Conversation.ID}); !sdk.IsStatus(err, 403) {
		t.Fatal("unreported source allowed", err)
	}
	accept := func(d service.Device, c *sdk.Client, session string) {
		input, err := e.st.DispatchMessage(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		run := uuid.NewString()
		event, _ := json.Marshal(map[string]any{"type": "accepted", "run_id": run, "input_id": "initial", "text": input.Content})
		if err = e.st.ReportClientEvent(ctx, d.ID, protocol.ClientEvent{ConversationID: input.ConversationID, MessageID: input.MessageID, AgentID: input.AgentID, AgentVersionID: input.AgentVersionID, Revision: "test", SessionID: session, Seq: 1, Event: event}); err != nil {
			t.Fatal(err)
		}
		c.Source = &sdk.ConversationSource{ConversationID: input.ConversationID, AgentID: input.AgentID, SessionID: session, RunID: run}
	}
	accept(d, parentClient, "parent-session")
	child, err := parentClient.CreateConversation(ctx, "", sdk.CreateConversationRequest{Mode: "single", ParentConversationID: parent.Conversation.ID, Prompt: "implement", ToAgentIDs: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if child.Conversation.ParentAgentID != a.ID || child.Message.AuthorAgentID != a.ID {
		t.Fatal(child)
	}
	accept(other, childClient, "child-session")
	for _, client := range []*sdk.Client{human, childClient} {
		if _, err = client.CreateConversation(ctx, "", sdk.CreateConversationRequest{ParentConversationID: parent.Conversation.ID}); !sdk.IsStatus(err, 403) {
			t.Fatal("source bypass", err)
		}
	}
	if _, err = childClient.CreateConversation(ctx, "", sdk.CreateConversationRequest{}); !sdk.IsStatus(err, 403) {
		t.Fatal("leaf created root", err)
	}
	if _, err = childClient.ListAgents(ctx, ""); !sdk.IsStatus(err, 403) {
		t.Fatal("leaf exposed agent selection", err)
	}
	if list, err := parentClient.ListAgents(ctx, ""); err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	if list, err := parentClient.ListChildren(ctx, "", parent.Conversation.ID); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if _, err = parentClient.ListConversations(ctx, ""); !sdk.IsStatus(err, 403) {
		t.Fatal("device listed unrelated roots", err)
	}
	if _, err = childClient.GetConversation(ctx, "", parent.Conversation.ID); !sdk.IsStatus(err, 403) {
		t.Fatal("ordinary leaf parent read bypass", err)
	}
	feedback, err := childClient.SendParentMessage(ctx, "", child.Conversation.ID, sdk.ParentMessageRequest{Prompt: "Which target?"})
	if err != nil || feedback.Delivery != "delivered" {
		t.Fatal(feedback, err)
	}
	if _, err = parentClient.SendMessage(ctx, "", child.Conversation.ID, sdk.MessageRequest{Prompt: "Use main", ToAgentIDs: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}
	saved := *parentClient.Source
	parentClient.Source.SessionID = "forged"
	if _, err = parentClient.ListChildren(ctx, "", parent.Conversation.ID); !sdk.IsStatus(err, 403) {
		t.Fatal("forged session allowed", err)
	}
	parentClient.Source = &saved
	// Each conversation owns a separate file tree. Related reads need verified source.
	input, err := human.UploadAttachment(ctx, "", parent.Conversation.ID, "input.txt", bytes.NewBufferString("human input"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := childClient.UploadAttachment(ctx, "", child.Conversation.ID, "report.txt", bytes.NewBufferString("child output"))
	if err != nil {
		t.Fatal(err)
	}
	if files, err := childClient.AllAttachments(ctx, "", parent.Conversation.ID); err != nil || len(files) != 1 || files[0].ID != input.ID {
		t.Fatal(files, err)
	}
	if files, err := parentClient.AllAttachments(ctx, "", child.Conversation.ID); err != nil || len(files) != 1 || files[0].ID != output.ID {
		t.Fatal(files, err)
	}
	body, err := parentClient.DownloadAttachment(ctx, "", child.Conversation.ID, output.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	body.Close()
	if string(data) != "child output" {
		t.Fatal(string(data))
	}
	unrelated, err := human.CreateConversation(ctx, "", sdk.CreateConversationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parentClient.AllAttachments(ctx, "", unrelated.Conversation.ID); !sdk.IsStatus(err, 404) {
		t.Fatal("unrelated files read", err)
	}
	sibling, err := parentClient.CreateConversation(ctx, "", sdk.CreateConversationRequest{ParentConversationID: parent.Conversation.ID, Prompt: "sibling task", ToAgentIDs: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for {
		input, e2 := e.st.DispatchMessage(ctx, other)
		if e2 != nil {
			t.Fatal(e2)
		}
		if input.ConversationID == sibling.Conversation.ID {
			break
		}
	}
	if _, err = childClient.AllAttachments(ctx, "", sibling.Conversation.ID); !sdk.IsStatus(err, 404) {
		t.Fatal("sibling files read", err)
	}
	for _, path := range []string{"/api/works", "/api/works/example", "/api/agent-tools/work.py"} {
		if code := e.code(token, "GET", path, nil); code != 404 {
			t.Fatal("obsolete route exists", path, code)
		}
	}
	if code := e.code(token, "POST", "/api/conversations", map[string]any{"parent_conversation_id": parent.Conversation.ID, "parent_agent_id": a.ID}); code != 400 {
		t.Fatal("forged lineage field accepted", code)
	}
}
