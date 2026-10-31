package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/internal/attachment"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
)

func enableRuns(t *testing.T, e *env, u service.User, d service.Daemon) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.st.DaemonConnected(ctx, d, []service.ReportedHarness{{Name: "claude"}}, []string{service.RunCapability, attachment.Capability}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetDaemonHarnessEnabled(ctx, u, d.ID, "claude", true); err != nil {
		t.Fatal(err)
	}
}
func multiSession(t *testing.T, f *attachmentFixture) service.Session {
	t.Helper()
	var res struct{ Session service.Session }
	r := f.e.req(f.token, "POST", "/api/sessions", map[string]any{"mode": service.MultiAgent}, &res)
	if r.StatusCode != 201 || res.Session.Mode != service.MultiAgent {
		t.Fatalf("multi session %d %+v", r.StatusCode, res)
	}
	return res.Session
}
func sendMulti(t *testing.T, f *attachmentFixture, id, prompt string, targets []string) service.MultiReply {
	t.Helper()
	var reply service.MultiReply
	r := f.e.req(f.token, "POST", "/api/sessions/"+id+"/messages", map[string]any{"prompt": prompt, "target_agent_ids": targets}, &reply)
	if r.StatusCode != 201 {
		t.Fatalf("send %d %+v", r.StatusCode, reply)
	}
	return reply
}
func finishMulti(t *testing.T, e *env, cl service.RunClaim, output string) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.ReportRunEvent(ctx, cl.Run.DaemonID, cl.Run.ID, cl.Run.AttemptID, 1, "started", json.RawMessage(`{}`), 60); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"ok": true, "output": output})
	if err := e.st.ReportRunEvent(ctx, cl.Run.DaemonID, cl.Run.ID, cl.Run.AttemptID, 2, "done", b, 60); err != nil {
		t.Fatal(err)
	}
}
func TestMultiAgentRoutingAndHistory(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	e := f.e
	ctx := context.Background()
	enableRuns(t, e, f.u, f.d)
	bob, bobTok := e.user("bob", service.RoleUser)
	dB := e.daemonWith(bob, "claude")
	enableRuns(t, e, bob, dB)
	aB, err := e.st.CreateAgent(ctx, bob.ID, "reviewer", "reviewer", dB.ID, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	ss := multiSession(t, f)
	path := "/api/sessions/" + ss.ID
	note := sendMulti(t, f, ss.ID, "shared note", nil)
	if len(note.Runs) != 0 || len(note.Deliveries) != 0 {
		t.Fatal("implicit agent")
	}
	first := sendMulti(t, f, ss.ID, "review", []string{f.agent, aB.ID, f.agent})
	if len(first.Runs) != 2 || len(first.Deliveries) != 2 {
		t.Fatalf("targets %+v", first)
	}
	clA, err := e.st.ClaimRun(ctx, f.d, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(clA.Messages) != 2 || clA.Messages[0].ID != note.Message.ID {
		t.Fatalf("shared history %+v", clA.Messages)
	}
	second := sendMulti(t, f, ss.ID, "also fix docs", []string{f.agent, aB.ID})
	if len(second.Runs) != 0 || len(second.Deliveries) != 2 {
		t.Fatalf("busy route %+v", second)
	}
	if err = e.st.ReportRunEvent(ctx, f.d.ID, clA.Run.ID, clA.Run.AttemptID, 1, "started", json.RawMessage(`{}`), 60); err != nil {
		t.Fatal(err)
	}
	var steerID int64
	for _, d := range second.Deliveries {
		if d.AgentID == f.agent {
			steerID = d.ID
		}
	}
	ack, _ := json.Marshal(map[string]int64{"steer_id": steerID})
	if err = e.st.ReportRunEvent(ctx, f.d.ID, clA.Run.ID, clA.Run.AttemptID, 2, "steered", ack, 60); err != nil {
		t.Fatal(err)
	}
	done := json.RawMessage(`{"ok":true,"output":"coder done"}`)
	for i := 0; i < 2; i++ {
		if err = e.st.ReportRunEvent(ctx, f.d.ID, clA.Run.ID, clA.Run.AttemptID, 3, "done", done, 60); err != nil {
			t.Fatal(err)
		}
	}
	clB, err := e.st.ClaimRun(ctx, dB, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(clB.TaskMessageIDs) != 2 {
		t.Fatalf("pending supplements %+v", clB.TaskMessageIDs)
	}
	found := false
	for _, m := range clB.Messages {
		if m.AgentKey == f.agent && strings.Contains(string(m.Content), "coder done") {
			found = true
		}
	}
	if !found {
		t.Fatal("reviewer missing coder result")
	}
	if e.code(bobTok, "GET", path, nil) != 404 {
		t.Fatal("external agent granted human read")
	}
	finishMulti(t, e, clB, "reviewer done")
	runs, err := e.st.Runs(ctx, ss.ID)
	if err != nil || len(runs) != 2 {
		t.Fatal("duplicate run", runs, err)
	}
	for _, r := range runs {
		if r.Status != "succeeded" {
			t.Fatal(r)
		}
	}
	ds, _ := e.st.Deliveries(ctx, ss.ID)
	for _, d := range ds {
		if d.Status != "succeeded" {
			t.Fatal("unfinished delivery", d)
		}
	}
	next := sendMulti(t, f, ss.ID, "next", []string{f.agent})
	if len(next.Runs) != 1 {
		t.Fatal(next)
	}
	clNext, err := e.st.ClaimRun(ctx, f.d, 60)
	if err != nil {
		t.Fatal(err)
	}
	if clNext.Run.ID == clA.Run.ID || len(clNext.Messages) != 6 {
		t.Fatalf("next history %d", len(clNext.Messages))
	}
}
func TestMultiInputQueueAndFencing(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	e := f.e
	enableRuns(t, e, f.u, f.d)
	ss := multiSession(t, f)
	f.session = ss
	first := sendMulti(t, f, ss.ID, "first", []string{f.agent})
	cl, err := e.st.ClaimRun(ctx, f.d, 60)
	if err != nil {
		t.Fatal(err)
	}
	var custom service.MultiReply
	e.req(f.token, "POST", "/api/sessions/"+ss.ID+"/messages", map[string]any{"input": map[string]string{"json": "keep"}, "target_agent_ids": []string{f.agent}}, &custom)
	third := sendMulti(t, f, ss.ID, "third", []string{f.agent})
	finishMulti(t, e, cl, "first done")
	cl2, err := e.st.ClaimRun(ctx, f.d, 60)
	if err != nil {
		t.Fatal(err)
	}
	if cl2.Run.MessageID != custom.Message.ID || len(cl2.TaskMessageIDs) != 1 || string(cl2.Input) != `{"json":"keep"}` {
		t.Fatalf("JSON queue %+v %s", cl2, string(cl2.Input))
	}
	finishMulti(t, e, cl2, "json done")
	cl3, err := e.st.ClaimRun(ctx, f.d, 60)
	if err != nil || cl3.Run.MessageID != third.Message.ID {
		t.Fatal("third", cl3, err)
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/sessions/"+ss.ID, nil)
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("X-Agentuse-Run", cl3.Run.ID)
	req.Header.Set("X-Agentuse-Attempt", cl3.Run.AttemptID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("run read", resp.StatusCode)
	}
	if _, err = e.st.ReconcileRuns(ctx, f.d.ID, nil); err != nil {
		t.Fatal(err)
	}
	cl4, err := e.st.ClaimRun(ctx, f.d, 60)
	if err != nil || cl4.Run.ID != cl3.Run.ID || cl4.Run.AttemptID == cl3.Run.AttemptID {
		t.Fatal("reclaim", cl4, err)
	}
	if err = e.st.ReportRunEvent(ctx, f.d.ID, cl3.Run.ID, cl3.Run.AttemptID, 1, "done", json.RawMessage(`{"ok":true}`), 60); err != service.ErrStaleMessage {
		t.Fatal("old attempt accepted", err)
	}
	c := agentuse.Client{BaseURL: e.ts.URL, Token: f.key, RunID: cl3.Run.ID, AttemptID: cl3.Run.AttemptID}
	if _, err = c.UploadAttachment(ctx, "", ss.ID, "late.txt", strings.NewReader("x")); err == nil {
		t.Fatal("stale upload")
	}
	c.RunID = cl4.Run.ID
	c.AttemptID = cl4.Run.AttemptID
	a, err := c.UploadAttachment(ctx, "", ss.ID, "result.txt", strings.NewReader("data"))
	if err != nil || a.CreatedByRunID != cl4.Run.ID {
		t.Fatal("run upload", a, err)
	}
	cancel, err := e.st.CancelRun(ctx, f.u, ss.ID, cl4.Run.ID)
	if err != nil || cancel.Status != "cancelled" {
		t.Fatal(cancel, err)
	}
	if _, err = c.AllAttachments(ctx, "", ss.ID); err == nil {
		t.Fatal("ended run still reads")
	}
	runs, _ := e.st.Runs(ctx, ss.ID)
	if len(runs) != 3 || runs[0].MessageID != first.Message.ID {
		t.Fatal("unexpected runs", runs)
	}
}
func TestMultiAtomicIdempotency(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx := context.Background()
	e := f.e
	enableRuns(t, e, f.u, f.d)
	ss := multiSession(t, f)
	for i := 0; i < 2; i++ {
		var reply service.MultiReply
		r := e.req(f.token, "POST", "/api/sessions/"+ss.ID+"/messages", map[string]any{"prompt": "same", "target_agent_ids": []string{f.agent}}, &reply, "Idempotency-Key", "test")
		if r.StatusCode != 201 || len(reply.Runs) != 1 {
			t.Fatal("idempotency", r.StatusCode, reply)
		}
	}
	if e.req(f.token, "POST", "/api/sessions/"+ss.ID+"/messages", map[string]any{"prompt": "different", "target_agent_ids": []string{f.agent}}, nil, "Idempotency-Key", "test").StatusCode != 409 {
		t.Fatal("key mismatch")
	}
	if e.code(f.token, "POST", "/api/sessions/"+ss.ID+"/messages", map[string]any{"prompt": "invalid", "target_agent_ids": []string{f.agent, "missing"}}) != 400 {
		t.Fatal("invalid target")
	}
	msgs, _ := e.st.Messages(ctx, ss.ID)
	if len(msgs) != 1 {
		t.Fatal("partial write")
	}
	f.session = ss
	if code, _ := rawUpload(t, f, f.token, "input.txt", "input", nil); code != 201 {
		t.Fatal("unbound upload", code)
	}
}

func TestMultiWorkAttachmentAttribution(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	e := f.e
	ctx := context.Background()
	enableRuns(t, e, f.u, f.d)
	wk, coor, err := e.st.CreateMultiWork(ctx, f.u, "work", f.agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.SendMultiMessage(ctx, f.u, coor.ID, json.RawMessage(`{"prompt":"coordinate"}`), []string{f.agent}, ""); err != nil {
		t.Fatal(err)
	}
	cl, err := e.st.ClaimRun(ctx, f.d, 60)
	if err != nil || cl.WorkToken == "" {
		t.Fatal(cl, err)
	}
	f.session = coor
	code, a := rawUpload(t, f, cl.WorkToken, "report.txt", "work result", nil)
	if code != 201 || a.CreatedByRunID != cl.Run.ID || a.CreatedByAgentKey != f.agent || a.Source != "work" {
		t.Fatalf("work author %d %+v", code, a)
	}
	stored, err := e.st.GetAttachment(ctx, coor.ID, a.ID)
	if err != nil || stored.CreatedByAgentKey != f.agent {
		t.Fatal(stored, err)
	}
	thread, _, err := e.st.CreateMultiSession(ctx, f.u, wk.ID, "thread", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	f.session = thread
	if code, a = rawUpload(t, f, cl.WorkToken, "input.txt", "thread input", nil); code != 201 || a.CreatedByRunID != cl.Run.ID || a.CreatedByAgentKey != f.agent {
		t.Fatalf("cross-thread attribution %d %+v", code, a)
	}
	finishMulti(t, e, cl, "finished")
	if code, _ = rawUpload(t, f, cl.WorkToken, "late.txt", "late", nil); code != 401 {
		t.Fatal("expired coordinator", code)
	}
}
func TestMultiIdempotencyCanonicalJSON(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ss := multiSession(t, f)
	path := "/api/sessions/" + ss.ID + "/messages"
	var first, second service.MultiReply
	f.e.req(f.token, "POST", path, `{"input":{"a":1,"b":2}}`, &first, "Idempotency-Key", "canonical")
	resp := f.e.req(f.token, "POST", path, `{"input": {"b":2,"a":1}}`, &second, "Idempotency-Key", "canonical")
	if resp.StatusCode != 201 || first.Message.ID != second.Message.ID {
		t.Fatal("JSON retry mismatch", resp.StatusCode, first, second)
	}
}

func TestMultiDaemonResetRevokesWorkExecution(t *testing.T) {
	for _, operation := range []string{"reset", "delete"} {
		t.Run(operation, func(t *testing.T) {
			f := attachmentEnv(t, 1024, 4096)
			e := f.e
			ctx := context.Background()
			enableRuns(t, e, f.u, f.d)
			_, coor, err := e.st.CreateMultiWork(ctx, f.u, "revocation", f.agent, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.st.SendMultiMessage(ctx, f.u, coor.ID, json.RawMessage(`{"prompt":"work"}`), []string{f.agent}, ""); err != nil {
				t.Fatal(err)
			}
			cl, err := e.st.ClaimRun(ctx, f.d, 60)
			if err != nil || cl.WorkToken == "" {
				t.Fatal(cl, err)
			}
			if operation == "reset" {
				_, err = e.st.ResetDaemonSetup(ctx, f.u, f.d.ID)
			} else {
				err = e.st.DeleteDaemon(ctx, f.u, f.d.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := e.st.GetRun(ctx, f.u, coor.ID, cl.Run.ID)
			if err != nil || r.Status != "failed" {
				t.Fatal("run not revoked", r, err)
			}
			if e.code(cl.WorkToken, "GET", "/api/agents", nil) != 401 {
				t.Fatal("revoked work token still authorized")
			}
			if err = e.st.ReportRunEvent(ctx, f.d.ID, cl.Run.ID, cl.Run.AttemptID, 99, "done", json.RawMessage(`{"ok":true}`), 60); err == nil {
				t.Fatal("revoked result accepted")
			}
		})
	}
}

func TestSharedSessionIsOnlyModel(t *testing.T) {
	e := newEnv(t, Config{})
	_, token := e.user("alice", service.RoleUser)
	var out struct{ Session service.Session }
	if code := e.req(token, "POST", "/api/sessions", map[string]any{}, &out).StatusCode; code != 201 || out.Session.Mode != service.MultiAgent || out.Session.Status != "idle" {
		t.Fatalf("default %d %+v", code, out)
	}
	for _, body := range []map[string]any{{"mode": "legacy"}, {"agent_id": "coder"}, {"mode": "unknown"}} {
		if code := e.code(token, "POST", "/api/sessions", body); code != 400 {
			t.Fatalf("obsolete request accepted: %d", code)
		}
	}
	if code := e.code(token, "POST", "/api/sessions/"+out.Session.ID+"/upgrade", map[string]any{}); code != 404 && code != 405 {
		t.Fatalf("upgrade route remains: %d", code)
	}
	if code := e.code(token, "POST", "/api/sessions/"+out.Session.ID+"/messages", map[string]any{"prompt": "note"}); code != 201 {
		t.Fatalf("note %d", code)
	}
	var detail map[string]json.RawMessage
	e.req(token, "GET", "/api/sessions/"+out.Session.ID, nil, &detail)
	var session map[string]any
	json.Unmarshal(detail["session"], &session)
	for _, field := range []string{"agent_id", "daemon_id", "harness", "harness_session_id", "lease_until"} {
		if _, ok := session[field]; ok {
			t.Fatalf("bound runtime field %s", field)
		}
	}
}
