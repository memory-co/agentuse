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
	deviceservice "github.com/memory-co/agentuse/server/internal/service/device"
)

func TestNestedConversationAPISourceScopesAndIndependentFiles(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	ctx := context.Background()
	u, token := e.user("nested-owner", sdk.RoleUser)
	human := sdk.New(e.ts.URL, token)
	device := func(name string) (sdk.Device, *sdk.Client) {
		d, err := e.st.DeviceService.CreateDevice(ctx, u.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		d, _, err = enrollDevice(t, e.st, d.SetupToken, "")
		key := "dk_retired"
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.st.DeviceService.DeviceConnected(ctx, d, []deviceservice.ReportedHarness{{Name: "codex"}}); err != nil {
			t.Fatal(err)
		}
		if err = e.st.DeviceService.SetDeviceHarnessEnabled(ctx, u, d.ID, "codex", true); err != nil {
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
	if _, err = parentClient.CreateConversation(ctx, "", sdk.CreateConversationRequest{ParentConversationID: parent.Conversation.ID}); !sdk.IsStatus(err, 401) {
		t.Fatal("unreported source allowed", err)
	}
	accept := func(d sdk.Device, c *sdk.Client, session string) {
		input, err := e.st.RunService.DispatchMessage(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		run := uuid.NewString()
		event, _ := json.Marshal(map[string]any{"type": "accepted", "run_id": run, "input_id": "initial", "text": input.Content})
		if err = e.st.ConversationService.ReportClientEvent(ctx, d.ID, protocol.ClientEvent{AccessToken: input.Access.Token, ConversationID: input.ConversationID, MessageID: input.MessageID, AgentID: input.AgentID, AgentVersionID: input.AgentVersionID, Revision: "test", SessionID: session, Seq: 1, Event: event}); err != nil {
			t.Fatal(err)
		}
		c.Token = input.Access.Token
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
	renameBody := map[string]string{"title": "New child title"}
	childPath := "/api/conversations/" + child.Conversation.ID
	if code := e.code(childClient.Token, "PATCH", childPath, renameBody); code != 401 {
		t.Fatal("execution token can rename conversation", code)
	}
	if code := e.code(token, "PATCH", childPath, renameBody); code != 200 {
		t.Fatal("owner could not rename child conversation", code)
	}
	children, err := human.ListChildren(ctx, "", parent.Conversation.ID)
	if err != nil || len(children) != 1 || children[0].ID != child.Conversation.ID || children[0].Title != renameBody["title"] {
		t.Fatal("rename not reflected in children list", children, err)
	}
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
	if code := e.code(childClient.Token, "POST", "/api/conversations/"+child.Conversation.ID+"/parent/messages", map[string]any{"prompt": "removed"}); code != 404 {
		t.Fatal("manual parent endpoint remains", code)
	}
	round, err := parentClient.GetConversationRound(ctx, "", child.Conversation.ID, 1)
	if err != nil || len(round.Rounds) != 1 || round.Rounds[0].Seq != 1 || len(round.Messages) != 1 || len(round.Runs) != 1 {
		t.Fatal(round, err)
	}
	if _, err = childClient.GetConversationRound(ctx, "", parent.Conversation.ID, 1); !sdk.IsStatus(err, 403) {
		t.Fatal("round read escaped leaf scope", err)
	}
	for _, query := range []string{"round=0", "round=-1", "round=abc", "round=1&round=2", "round=", "unknown=1"} {
		if code := e.code(token, "GET", "/api/conversations/"+child.Conversation.ID+"?"+query, nil); code != 400 {
			t.Fatal(query, code)
		}
	}
	if _, err = human.GetConversationRound(ctx, "", child.Conversation.ID, 2); !sdk.IsStatus(err, 404) {
		t.Fatal("missing round", err)
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
		input, e2 := e.st.RunService.DispatchMessage(ctx, other)
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
