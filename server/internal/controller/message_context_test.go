package controller

import (
	"context"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestMessageContextIsScopedToDeliveredDevice(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	_, err := f.e.st.SendMessage(ctx, f.u, f.conversation.ID, "shared-note", nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := f.e.st.SendMessage(ctx, f.u, f.conversation.ID, "current", []string{f.agent}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.e.st.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	client := agentuse.New(f.e.ts.URL, f.key)
	result, err := client.GetMessageContext(ctx, f.conversation.ID, p.MessageID, f.agent, "new-local-conversation")
	if err != nil || len(result.CompleteMessages) != 1 || result.CompleteMessages[0].Text != "shared-note" {
		t.Fatal(result, err)
	}
	if _, err = client.GetMessageContext(ctx, f.conversation.ID, reply.Message.ID, "wrong-agent", "new-local-conversation"); !agentuse.IsStatus(err, 404) {
		t.Fatal("cross-agent context", err)
	}
	other, err := f.e.st.CreateDevice(ctx, f.u.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := f.e.st.RegisterDevice(ctx, other.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = agentuse.New(f.e.ts.URL, key).GetMessageContext(ctx, f.conversation.ID, p.MessageID, f.agent, "new-local-conversation"); !agentuse.IsStatus(err, 404) {
		t.Fatal("unassigned device read context", err)
	}
	if _, err = agentuse.New(f.e.ts.URL, f.token).GetMessageContext(ctx, f.conversation.ID, p.MessageID, f.agent, "new-local-conversation"); !agentuse.IsStatus(err, 401) {
		t.Fatal("non-device context API accepted", err)
	}
	if _, err = client.GetMessageContext(ctx, f.conversation.ID, p.MessageID, f.agent, ""); !agentuse.IsStatus(err, 400) {
		t.Fatal("missing local conversation accepted", err)
	}
	legacy := "/api/conversations/" + f.conversation.ID + "/messages/" + p.MessageID + "/agents/" + f.agent + "/context?client_session_id=old"
	if code := f.e.code(f.key, "GET", legacy, nil); code != 400 {
		t.Fatal("old native session alias retained", code)
	}
}
