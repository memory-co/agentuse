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

func TestRunAPIUsesClientIdentityAndGenericReply(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	var reply service.MultiReply
	response := f.e.req(f.token, "POST", "/api/sessions/"+f.session.ID+"/messages", map[string]any{"prompt": "input", "target_agent_ids": []string{f.agent}}, &reply)
	if response.StatusCode != 201 || len(reply.Runs) != 0 || len(reply.Deliveries) != 1 {
		t.Fatal("server predicted run membership", response.StatusCode, reply)
	}
	p, err := f.e.st.ClaimPrompt(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	report := func(seq int64, event any) {
		data, _ := json.Marshal(event)
		if err := f.e.st.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{ConversationID: f.session.ID, SessionID: "native", AgentID: f.agent, Harness: p.Harness, Revision: "reported", PromptID: p.ID, Seq: seq, Event: data}); err != nil {
			t.Fatal(err)
		}
	}
	report(1, map[string]any{"type": "accepted", "run_id": run, "input_id": "input", "text": "input"})
	var detail service.SessionDetail
	if r := f.e.req(f.token, "GET", "/api/sessions/"+f.session.ID, nil, &detail); r.StatusCode != 200 || len(detail.Runs) != 1 || detail.Runs[0].ID != run {
		t.Fatal(r.StatusCode, detail)
	}
	raw, _ := json.Marshal(detail)
	for _, foreign := range []string{"attempt_id", "lease_until", "harness_run_ids", "agent_user_id"} {
		if strings.Contains(string(raw), foreign) {
			t.Fatal("foreign execution identity in API", foreign)
		}
	}
	report(2, map[string]any{"type": "request", "run_id": run, "request_id": "request", "state": "pending", "kind": "permission", "message": "Proceed?"})
	if code := f.e.code(f.token, "POST", "/api/sessions/"+f.session.ID+"/runs/"+run+"/requests/request/reply", map[string]any{"answer": "deny"}); code != http.StatusAccepted {
		t.Fatal(code)
	}
	controls, err := f.e.st.PendingControls(ctx, f.d.ID)
	if err != nil || len(controls) != 1 || string(controls[0].Control.Answer) != `"deny"` {
		t.Fatal(controls, err)
	}
	if code := f.e.code(f.token, "POST", "/api/sessions/"+f.session.ID+"/runs/"+run+"/cancel", map[string]any{}); code != 200 {
		t.Fatal(code)
	}
	r, err := f.e.st.GetRun(ctx, f.u, f.session.ID, run)
	if err != nil || r.Status != "waiting" {
		t.Fatal("cancel request fabricated terminal state", r, err)
	}
	report(3, map[string]any{"type": "result", "run_id": run, "status": "cancelled", "input_ids": []string{"input"}, "output": ""})
	r, _ = f.e.st.GetRun(ctx, f.u, f.session.ID, run)
	if r.Status != "cancelled" {
		t.Fatal(r)
	}
}
func TestMessageSubmissionDoesNotDeduplicateUserText(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ids := map[int64]bool{}
	for i := 0; i < 2; i++ {
		var reply service.MultiReply
		r := f.e.req(f.token, "POST", "/api/sessions/"+f.session.ID+"/messages", map[string]any{"prompt": "same", "target_agent_ids": []string{f.agent}}, &reply)
		if r.StatusCode != 201 || reply.Message == nil || ids[reply.Message.ID] {
			t.Fatal(r.StatusCode, reply)
		}
		ids[reply.Message.ID] = true
	}
}
