package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

func TestWorkToolAuthorizationBelongsToServer(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	work, coordinator, err := f.e.st.CreateMultiWork(ctx, f.u, "cross-device work", f.agent, "instructions")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.e.st.SendMultiMessage(ctx, f.u, coordinator.ID, json.RawMessage(`{"prompt":"coordinate"}`), []string{f.agent}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	input, err := f.e.st.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	for _, business := range []string{`"work_id"`, `"work_token"`} {
		if strings.Contains(string(raw), business) {
			t.Fatal("business credential in execution protocol", business)
		}
	}
	run := uuid.NewString()
	identity := workToolAuthorization{ConversationID: coordinator.ID, MessageID: input.MessageID, AgentID: f.agent, RunID: run}
	runIdentity := identity
	runIdentity.MessageID = ""
	path := "/api/agent-tools/work/authorize"
	if status := f.e.code(f.key, "POST", path, runIdentity); status != 403 {
		t.Fatal("unreported run authorized without a source mapping", status)
	}
	if status := f.e.code(f.token, "POST", path, identity); status != 401 {
		t.Fatal("user credential authorized an execution", status)
	}
	other, err := f.e.st.CreateDaemon(ctx, f.u.ID, "another device")
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, err := f.e.st.RegisterDaemon(ctx, other.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if status := f.e.code(otherKey, "POST", path, identity); status != 403 {
		t.Fatal("other device authorized a coordinator", status)
	}
	if response := f.e.req(f.key, "POST", path, identity, nil, actAsHeader, f.u.ID); response.StatusCode != 401 {
		t.Fatal("device impersonation accepted", response.StatusCode)
	}
	var authorization workToolCredential
	if response := f.e.req(f.key, "POST", path, identity, &authorization); response.StatusCode != 200 || authorization.Token == "" {
		t.Fatal("in-flight accepted authorization", response.StatusCode)
	}
	message, err := f.e.st.PendingMessages(ctx, f.d.ID)
	if err != nil || len(message) != 1 || message[0].MessageID != input.MessageID {
		t.Fatal(message, err)
	}
	report := func(seq int64, event any) {
		data, _ := json.Marshal(event)
		err := f.e.st.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{ConversationID: coordinator.ID, SessionID: "native", AgentID: f.agent, AgentVersionID: input.AgentVersionID, Revision: "test", MessageID: input.MessageID, Seq: seq, Event: data})
		if err != nil {
			t.Fatal(err)
		}
	}
	report(1, map[string]any{"type": "accepted", "run_id": run, "input_id": "first", "text": "coordinate"})
	if response := f.e.req(f.key, "POST", path, runIdentity, &authorization); response.StatusCode != 200 || authorization.Token == "" {
		t.Fatal("Server failed to resolve source message from run", response.StatusCode)
	}
	if status := f.e.code(otherKey, "POST", path, runIdentity); status != 403 {
		t.Fatal("run mapping bypassed device ownership", status)
	}
	wrong := identity
	wrong.RunID = uuid.NewString()
	if status := f.e.code(f.key, "POST", path, wrong); status != 403 {
		t.Fatal("different run authorized", status)
	}
	if status := f.e.code(f.key, "POST", path, identity); status != 200 {
		t.Fatal("accepted run rejected", status)
	}
	var created struct {
		Session service.Session `json:"session"`
	}
	if response := f.e.req(authorization.Token, "POST", "/api/sessions", map[string]any{}, &created); response.StatusCode != 201 || created.Session.WorkID != work.ID {
		t.Fatal("tool authorization escaped work", response.StatusCode, created)
	}
	foreign, _, err := f.e.st.CreateMultiWork(ctx, f.u, "other work", f.agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if status := f.e.code(authorization.Token, "POST", "/api/sessions", map[string]any{"work_id": foreign.ID}); status != 403 {
		t.Fatal("cross-work tool write", status)
	}
	_, err = f.e.st.SendMultiMessage(ctx, f.u, f.session.ID, json.RawMessage(`{"prompt":"ordinary"}`), []string{f.agent}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryMessage, err := f.e.st.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := workToolAuthorization{ConversationID: f.session.ID, MessageID: ordinaryMessage.MessageID, AgentID: f.agent, RunID: uuid.NewString()}
	if status := f.e.code(f.key, "POST", path, ordinary); status != 403 {
		t.Fatal("ordinary conversation gained work capability", status)
	}
	report(2, map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"first"}, "output": "done"})
	if status := f.e.code(f.key, "POST", path, runIdentity); status != 403 {
		t.Fatal("terminal run authorized through source lookup", status)
	}
	if status := f.e.code(f.key, "POST", path, identity); status != 403 {
		t.Fatal("terminal run obtained authorization", status)
	}
	if status := f.e.code(authorization.Token, "GET", "/api/sessions", nil); status != 401 {
		t.Fatal("terminal capability survived", status)
	}
}

func TestWorkManagementRoutesKeepOwnerAndThreadScope(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	work, _, err := f.e.st.CreateMultiWork(ctx, f.u, "owner work", f.agent, "")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := f.e.st.CreateMultiWork(ctx, f.u, "other work", f.agent, "")
	if err != nil {
		t.Fatal(err)
	}
	_, stranger := f.e.user("stranger", service.RoleUser)
	_, admin := f.e.user("administrator", service.RoleAdmin)
	path := "/api/works/" + work.ID + "/threads"
	if status := f.e.code(stranger, "GET", path, nil); status != 404 {
		t.Fatal("another user's work readable", status)
	}
	var created struct {
		Session service.Session `json:"session"`
	}
	if response := f.e.req(admin, "POST", path, map[string]any{}, &created); response.StatusCode != 201 || created.Session.UserID != f.u.ID || created.Session.WorkID != work.ID {
		t.Fatal("admin changed work ownership", response.StatusCode, created)
	}
	if status := f.e.code(f.token, "GET", "/api/works/"+other.ID+"/threads/"+created.Session.ID, nil); status != 403 {
		t.Fatal("cross-work read", status)
	}
	if status := f.e.code(f.token, "POST", path+"/"+created.Session.ID+"/messages", map[string]any{"prompt": "note"}); status != 201 {
		t.Fatal(status)
	}
	if status := f.e.code(f.token, "POST", path+"/"+created.Session.ID+"/resolve", nil); status != 200 {
		t.Fatal(status)
	}
}

func TestServerWorkToolIsDownloadableWithoutCredentials(t *testing.T) {
	e := newEnv(t, Config{})
	response, err := http.Get(e.ts.URL + "/api/agent-tools/work.py")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/x-python") {
		t.Fatal(response.StatusCode, response.Header)
	}
}
