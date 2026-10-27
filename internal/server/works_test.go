package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/memory-co/agentuse/internal/store"
)

func TestWorksAPI(t *testing.T) {
	e := newEnv(t, Config{})
	ctx := context.Background()
	alice, aliceTok := e.user("alice", store.RoleUser)
	_, bobTok := e.user("bob", store.RoleUser)
	d := e.daemonWith(alice, "claude")
	e.st.CreateAgent(ctx, alice.ID, "coder", "写代码", d.ID, "claude", "")

	if c := e.code(aliceTok, "POST", "/api/works", map[string]string{"name": "w"}); c != http.StatusBadRequest {
		t.Fatalf("没有 agent_id: %d", c)
	}
	var created struct {
		Work        store.Work    `json:"work"`
		Coordinator store.Session `json:"coordinator"`
	}
	if r := e.req(aliceTok, "POST", "/api/works", map[string]string{"name": "重构", "agent_id": "coder", "instructions": "说明"}, &created); r.StatusCode != http.StatusCreated || created.Coordinator.WorkRole != store.WorkCoordinator {
		t.Fatalf("create work: %d %+v", r.StatusCode, created)
	}
	wid := created.Work.ID
	if c := e.code(bobTok, "GET", "/api/works/"+wid, nil); c != http.StatusNotFound {
		t.Fatalf("bob get work: %d", c)
	}
	var w store.Work
	if r := e.req(aliceTok, "PATCH", "/api/works/"+wid, map[string]string{"name": "改名"}, &w); r.StatusCode != 200 || w.Name != "改名" || w.Instructions != "说明" {
		t.Fatalf("patch: %d %+v", r.StatusCode, w)
	}

	// 线程只能由协调者用工作令牌开，用户令牌带 work_id 不行
	if c := e.code(aliceTok, "POST", "/api/sessions", map[string]string{"agent_id": "coder", "prompt": "x", "work_id": wid}); c != http.StatusForbidden {
		t.Fatalf("用户手动开线程: %d", c)
	}
	if c := e.code(aliceTok, "POST", "/api/sessions/"+created.Coordinator.ID+"/resolve", nil); c != http.StatusBadRequest {
		t.Fatalf("协调者不能标记完成: %d", c)
	}

	// 工作令牌：协调者被领走时才有，只能动这个工作的线程
	e.st.AddMessage(ctx, alice, created.Coordinator.ID, json.RawMessage(`{"prompt":"拆活"}`))
	cl, err := e.st.ClaimSession(ctx, d, 60)
	if err != nil || cl.Session.ID != created.Coordinator.ID {
		t.Fatalf("claim coordinator: %v %+v", err, cl)
	}
	wt := cl.WorkToken
	if c := e.code(wt, "GET", "/api/me", nil); c != http.StatusForbidden {
		t.Fatalf("工作令牌调别的接口: %d", c)
	}
	if c := e.code(wt, "GET", "/api/agents", nil); c != http.StatusOK {
		t.Fatalf("工作令牌列 agent: %d", c)
	}
	var th struct {
		Session store.Session `json:"session"`
	}
	if r := e.req(wt, "POST", "/api/sessions", map[string]string{"agent_id": "coder", "prompt": "y"}, &th); r.StatusCode != http.StatusCreated || th.Session.WorkID != wid || th.Session.WorkRole != store.WorkThread || th.Session.CreatedBy != alice.ID {
		t.Fatalf("工作令牌开线程: %d %+v", r.StatusCode, th)
	}
	// 普通列表里看不到线程，按 work_id 能看到
	var list []store.Session
	if e.req(aliceTok, "GET", "/api/sessions", nil, &list); len(list) != 0 {
		t.Fatalf("普通列表: %+v", list)
	}
	if e.req(aliceTok, "GET", "/api/sessions?work_id="+wid, nil, &list); len(list) != 1 {
		t.Fatalf("线程列表: %+v", list)
	}
	if e.req(wt, "GET", "/api/sessions", nil, &list); len(list) != 1 || list[0].ID != th.Session.ID {
		t.Fatalf("工作令牌列线程: %+v", list)
	}
	if c := e.code(bobTok, "GET", "/api/sessions?work_id="+wid, nil); c != http.StatusNotFound {
		t.Fatalf("bob 列线程: %d", c)
	}
	if c := e.code(aliceTok, "POST", "/api/sessions/"+th.Session.ID+"/resolve", nil); c != http.StatusConflict {
		t.Fatalf("pending 不能标记完成: %d", c)
	}
	if c := e.code(wt, "GET", "/api/sessions/"+th.Session.ID, nil); c != http.StatusOK {
		t.Fatalf("工作令牌看线程: %d", c)
	}
	if c := e.code(wt, "GET", "/api/sessions/"+created.Coordinator.ID, nil); c != http.StatusForbidden {
		t.Fatalf("工作令牌看协调者: %d", c)
	}
	// 普通会话不归它管
	var plain struct {
		Session store.Session `json:"session"`
	}
	e.req(aliceTok, "POST", "/api/sessions", map[string]string{"agent_id": "coder", "prompt": "z"}, &plain)
	if c := e.code(wt, "POST", "/api/sessions/"+plain.Session.ID+"/messages", map[string]string{"prompt": "hi"}); c != http.StatusForbidden {
		t.Fatalf("工作令牌给普通会话发消息: %d", c)
	}
	if c := e.code(wt, "DELETE", "/api/sessions/"+th.Session.ID, nil); c != http.StatusForbidden {
		t.Fatalf("工作令牌删线程: %d", c)
	}
	// 这一轮结束，令牌失效
	e.st.AddEvent(ctx, d.ID, cl.Message.ID, store.EventDone, json.RawMessage(`{"ok":true}`), 60)
	if c := e.code(wt, "GET", "/api/sessions", nil); c != http.StatusUnauthorized {
		t.Fatalf("过期的工作令牌: %d", c)
	}

	if c := e.code(aliceTok, "DELETE", "/api/works/"+wid, nil); c != http.StatusNoContent {
		t.Fatalf("delete work: %d", c)
	}
	if c := e.code(aliceTok, "GET", "/api/sessions/"+th.Session.ID, nil); c != http.StatusNotFound {
		t.Fatalf("线程跟着删: %d", c)
	}
}
