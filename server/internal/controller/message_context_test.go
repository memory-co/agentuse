package controller

import (
	"context"
	"encoding/json"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestMessageContextIsScopedToDeliveredDevice(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	_, err := f.e.st.SendMultiMessage(ctx, f.u, f.session.ID, json.RawMessage(`{"prompt":"shared-note"}`), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := f.e.st.SendMultiMessage(ctx, f.u, f.session.ID, json.RawMessage(`{"prompt":"current"}`), []string{f.agent}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.e.st.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	client := agentuse.New(f.e.ts.URL, f.key)
	result, err := client.GetMessageContext(ctx, f.session.ID, p.MessageID, f.agent, "new-local-session")
	if err != nil || len(result.CompleteMessages) != 1 || result.CompleteMessages[0].Text != "shared-note" {
		t.Fatal(result, err)
	}
	if _, err = client.GetMessageContext(ctx, f.session.ID, reply.Message.ID, "wrong-agent", "new-local-session"); !agentuse.IsStatus(err, 404) {
		t.Fatal("cross-agent context", err)
	}
	other, err := f.e.st.CreateDaemon(ctx, f.u.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := f.e.st.RegisterDaemon(ctx, other.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = agentuse.New(f.e.ts.URL, key).GetMessageContext(ctx, f.session.ID, p.MessageID, f.agent, "new-local-session"); !agentuse.IsStatus(err, 404) {
		t.Fatal("unassigned device read context", err)
	}
	if _, err = agentuse.New(f.e.ts.URL, f.token).GetMessageContext(ctx, f.session.ID, p.MessageID, f.agent, "new-local-session"); !agentuse.IsStatus(err, 401) {
		t.Fatal("non-device context API accepted", err)
	}
	if _, err = client.GetMessageContext(ctx, f.session.ID, p.MessageID, f.agent, ""); !agentuse.IsStatus(err, 400) {
		t.Fatal("missing local session accepted", err)
	}
}
