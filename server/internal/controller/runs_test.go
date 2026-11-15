package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestRunAPIUsesClientIdentityAndGenericReply(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	var reply agentuse.Reply
	response := f.e.req(f.token, "POST", "/api/conversations/"+f.conversation.ID+"/messages", map[string]any{"prompt": "input", "to_agent_ids": []string{f.agent}}, &reply)
	if response.StatusCode != 201 || len(reply.Runs) != 0 || len(reply.RunInputs) != 0 {
		t.Fatal("server predicted run membership", response.StatusCode, reply)
	}
	p, err := f.e.st.RunService.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	report := func(seq int64, event any) {
		data, _ := json.Marshal(event)
		if err := f.e.st.ConversationService.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{ConversationID: f.conversation.ID, SessionID: "native", AgentID: f.agent, AgentVersionID: p.AgentVersionID, Revision: "reported", MessageID: p.MessageID, Seq: seq, Event: data}); err != nil {
			t.Fatal(err)
		}
	}
	report(1, map[string]any{"type": "accepted", "run_id": run, "input_id": "input", "text": "input"})
	var detail agentuse.ConversationDetail
	if r := f.e.req(f.token, "GET", "/api/conversations/"+f.conversation.ID, nil, &detail); r.StatusCode != 200 || len(detail.Runs) != 1 || detail.Runs[0].ID != run {
		t.Fatal(r.StatusCode, detail)
	}
	var execution agentuse.RunDetail
	if r := f.e.req(f.token, "GET", "/api/conversations/"+f.conversation.ID+"/runs/"+run, nil, &execution); r.StatusCode != 200 || len(execution.Inputs) != 1 || execution.Inputs[0].InputID != "input" || execution.Inputs[0].MessageID != reply.Message.ID {
		t.Fatal("Run details omitted accepted inputs", r.StatusCode, execution)
	}
	raw, _ := json.Marshal(detail)
	for _, foreign := range []string{"attempt_id", "lease_until", "harness_run_ids", "agent_user_id"} {
		if strings.Contains(string(raw), foreign) {
			t.Fatal("foreign execution identity in API", foreign)
		}
	}
	report(2, map[string]any{"type": "request", "run_id": run, "request_id": "request", "state": "pending", "kind": "permission", "message": "Proceed?"})
	if code := f.e.code(f.token, "POST", "/api/conversations/"+f.conversation.ID+"/runs/"+run+"/requests/request/reply", map[string]any{"answer": "deny"}); code != http.StatusAccepted {
		t.Fatal(code)
	}
	controls, err := f.e.st.RunService.PendingControls(ctx, f.d.ID)
	if err != nil || len(controls) != 1 || string(controls[0].Control.Answer) != `"deny"` {
		t.Fatal(controls, err)
	}
	if code := f.e.code(f.token, "POST", "/api/conversations/"+f.conversation.ID+"/runs/"+run+"/cancel", map[string]any{}); code != 200 {
		t.Fatal(code)
	}
	r, err := f.e.st.RunService.GetRun(ctx, f.u, f.conversation.ID, run)
	if err != nil || r.Status != "waiting" {
		t.Fatal("cancel request fabricated terminal state", r, err)
	}
	report(3, map[string]any{"type": "result", "run_id": run, "status": "cancelled", "input_ids": []string{"input"}, "output": "", "model": "native-default-model", "usage": map[string]int{"input_tokens": 0, "output_tokens": 3}})
	r, _ = f.e.st.RunService.GetRun(ctx, f.u, f.conversation.ID, run)
	if r.Status != "cancelled" {
		t.Fatal(r)
	}
	if response := f.e.req(f.token, "GET", "/api/conversations/"+f.conversation.ID+"/runs/"+run, nil, &execution); response.StatusCode != 200 || execution.Run.Model == nil || *execution.Run.Model != "native-default-model" {
		t.Fatal("Run API omitted the native model on cancellation", execution)
	}
	usage := execution.Run.Usage
	if usage == nil || usage.InputTokens == nil || *usage.InputTokens != 0 || usage.OutputTokens == nil || *usage.OutputTokens != 3 || usage.CacheReadTokens != nil || usage.CacheWriteTokens != nil {
		t.Fatal("Run API lost zero or unknown token measurements", usage)
	}
	spec := readServerAPISpec(t, f.e, f.token)
	modelSchema, _ := json.Marshal(spec.Components.Schemas["Run"]["properties"].(map[string]any)["model"])
	if !strings.Contains(string(modelSchema), "string") || !strings.Contains(string(modelSchema), "null") {
		t.Fatal("Run model must be nullable in OpenAPI", string(modelSchema))
	}
	usageSchema := spec.resolve(spec.Components.Schemas["Run"]["properties"].(map[string]any)["usage"].(map[string]any))
	if !reflect.DeepEqual(usageSchema["type"], []any{"object", "null"}) {
		t.Fatal("Run usage must be nullable in OpenAPI", usageSchema)
	}
	for _, field := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens"} {
		counter := usageSchema["properties"].(map[string]any)[field].(map[string]any)
		if !reflect.DeepEqual(counter["type"], []any{"integer", "null"}) || counter["minimum"] != float64(0) {
			t.Fatal("token measurements must be nullable non-negative integers", field, counter)
		}
	}
}
func TestMessageSubmissionDoesNotDeduplicateUserText(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		var reply agentuse.Reply
		r := f.e.req(f.token, "POST", "/api/conversations/"+f.conversation.ID+"/messages", map[string]any{"prompt": "same", "to_agent_ids": []string{f.agent}}, &reply)
		if r.StatusCode != 201 || reply.Message == nil || ids[reply.Message.ID] {
			t.Fatal(r.StatusCode, reply)
		}
		ids[reply.Message.ID] = true
	}
}
