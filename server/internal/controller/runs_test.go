package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

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
	var summary agentuse.ConversationDetail
	if response := f.e.req(f.token, "GET", "/api/conversations/"+f.conversation.ID+"?view=summary&round=1", nil, &summary); response.StatusCode != 200 ||
		len(summary.Runs) != 1 || !reflect.DeepEqual(summary.Runs[0].Usage, usage) || !reflect.DeepEqual(summary.Runs[0].Model, execution.Run.Model) ||
		len(summary.Events) != 0 || len(summary.RunInputs) != 0 || len(summary.Questions) != 0 || len(summary.Runs[0].Result) != 0 {
		t.Fatal("timeline summary lost metadata or included detail payloads", response.StatusCode, summary)
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

func TestRunEventPagesBoundPayloadAndPreserveFullReads(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	if _, err := f.e.st.ConversationService.SendMessage(ctx, f.u, f.conversation.ID, "First message", []string{f.agent}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	p, err := f.e.st.RunService.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	run, otherRun := uuid.NewString(), uuid.NewString()
	report := func(seq int64, event map[string]any) []byte {
		t.Helper()
		raw, _ := json.Marshal(event)
		if err := f.e.st.ConversationService.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{
			ConversationID: f.conversation.ID, SessionID: "native", AgentID: f.agent, AgentVersionID: p.AgentVersionID,
			MessageID: p.MessageID, Seq: seq, Event: raw,
		}); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	report(1, map[string]any{"type": "accepted", "run_id": run, "input_id": "input", "text": "First message"})
	report(2, map[string]any{"type": "log", "run_id": otherRun, "message": "unrelated event"})
	large := report(3, map[string]any{"type": "log", "run_id": run, "message": strings.Repeat("測", 9000)})
	report(4, map[string]any{"type": "request", "run_id": run, "request_id": "q1", "state": "pending", "kind": "permission"})

	conversation := "/api/conversations/" + f.conversation.ID
	path := conversation + "/runs/" + run
	var metadata, full agentuse.RunDetail
	if r := f.e.req(f.token, "GET", path+"?view=metadata", nil, &metadata); r.StatusCode != 200 || len(metadata.Events) != 0 || len(metadata.Inputs) != 1 || len(metadata.Questions) != 1 {
		t.Fatalf("unexpected lazy metadata: %d", r.StatusCode)
	}
	if r := f.e.req(f.token, "GET", path, nil, &full); r.StatusCode != 200 || len(full.Events) != 3 || len(full.Events[1].Payload) < len(large) || len(full.Questions) != 0 {
		t.Fatalf("legacy run detail lost data: %d", r.StatusCode)
	}
	var summary agentuse.ConversationDetail
	if r := f.e.req(f.token, "GET", conversation+"?view=summary", nil, &summary); r.StatusCode != 200 ||
		len(summary.Events) != 0 || len(summary.RunInputs) != 0 || len(summary.Questions) != 0 || len(summary.Messages) != 1 || len(summary.Runs) != 1 {
		t.Fatalf("unexpected timeline summary: %d", r.StatusCode)
	}
	eventsPath := path + "/events"
	var first, second agentuse.RunEventPage
	if r := f.e.req(f.token, "GET", eventsPath+"?limit=2", nil, &first); r.StatusCode != 200 || len(first.Events) != 2 ||
		first.Events[0].Kind != "accepted" || first.Events[1].Kind != "log" || len(first.Events[1].Payload) != 0 ||
		utf8.RuneCountInString(first.Events[1].PayloadPreview) != 4096 || first.NextCursor == nil || *first.NextCursor != first.Events[1].ID {
		t.Fatalf("first page or preview is incorrect: %d", r.StatusCode)
	}
	if r := f.e.req(f.token, "GET", eventsPath+"?limit=2&cursor="+*first.NextCursor, nil, &second); r.StatusCode != 200 ||
		len(second.Events) != 1 || second.Events[0].Kind != "request" || second.NextCursor != nil {
		t.Fatalf("next page does not continue the run's event sequence: %d", r.StatusCode)
	}
	for _, query := range []string{"limit=51", "cursor=-1", "cursor=1&cursor=2"} {
		if status := f.e.code(f.token, "GET", eventsPath+"?"+query, nil); status != 400 {
			t.Fatal(query, status)
		}
	}
	eventPath := eventsPath + "/" + first.Events[1].ID
	_, otherToken := f.e.user("unrelated-user", agentuse.RoleUser)
	if got := f.e.code(otherToken, "GET", eventPath, nil); got != 404 {
		t.Fatal("event payload exposed across users", got)
	}
	req, err := http.NewRequest("GET", f.e.ts.URL+eventPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	res, err := f.e.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 || string(raw) != string(large) || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatal("full event download differs", res.StatusCode, err)
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
