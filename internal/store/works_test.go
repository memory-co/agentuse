package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWorks(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	d := registered(t, s, alice, "laptop", "claude")
	s.SetDaemonHarnessEnabled(ctx, alice, d.ID, "claude", true)
	ag, _ := s.CreateAgent(ctx, alice.ID, "", "a", d.ID, "claude", "")

	if _, _, err := s.CreateWork(ctx, alice, " ", ag.ID, ""); err == nil {
		t.Fatal("名字为空应该报错")
	}
	if _, _, err := s.CreateWork(ctx, alice, "w", "", ""); err == nil {
		t.Fatal("没有 agent 应该报错")
	}
	bd := registered(t, s, bob, "desktop", "claude")
	s.SetDaemonHarnessEnabled(ctx, bob, bd.ID, "claude", true)
	bag, _ := s.CreateAgent(ctx, bob.ID, "", "b", bd.ID, "claude", "")
	if _, _, err := s.CreateWork(ctx, alice, "w", bag.ID, ""); err == nil {
		t.Fatal("协调者不能用别人的 agent")
	}
	w, coord, err := s.CreateWork(ctx, alice, "重构", ag.ID, "先读 README")
	if err != nil || coord.Status != SessionIdle || coord.WorkRole != WorkCoordinator || w.CoordinatorID != coord.ID || coord.WorkID != w.ID {
		t.Fatalf("create: %v %+v %+v", err, w, coord)
	}
	if _, err := s.GetWork(ctx, bob, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("别人的工作看不到: %v", err)
	}
	if _, _, err := s.CreateSessionIn(ctx, bob, w.ID, ag.ID, "", json.RawMessage(`{"prompt":"x"}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不能往别人的工作里开线程: %v", err)
	}

	// 普通会话列表里没有工作里的会话
	plain, _, _ := s.CreateSession(ctx, alice, ag.ID, "", json.RawMessage(`{"prompt":"x"}`))
	if l, _ := s.ListSessions(ctx, alice, 10); len(l) != 1 || l[0].ID != plain.ID {
		t.Fatalf("普通会话: %+v", l)
	}
	s.ClaimSession(ctx, d, 60)
	pm, _ := s.Messages(ctx, plain.ID)
	s.AddEvent(ctx, d.ID, pm[0].ID, EventDone, json.RawMessage(`{"ok":true}`), 60)

	// 用户先跟协调者说话;协调者领活时拿到说明和工作令牌
	_, cm, err := s.AddMessage(ctx, alice, coord.ID, json.RawMessage(`{"prompt":"拆一下"}`))
	if err != nil {
		t.Fatal(err)
	}
	cl, err := s.ClaimSession(ctx, d, 60)
	if err != nil || cl.Session.ID != coord.ID || cl.Instructions != "先读 README" || !strings.HasPrefix(cl.WorkToken, "wt_") {
		t.Fatalf("claim coordinator: %v %+v", err, cl)
	}
	gw, owner, err := s.WorkByToken(ctx, cl.WorkToken)
	if err != nil || gw.ID != w.ID || owner.ID != alice.ID {
		t.Fatalf("work token: %v %+v %+v", err, gw, owner)
	}

	// 协调者开线程
	th, tm, err := s.CreateSessionIn(ctx, owner, w.ID, ag.ID, "改代码", json.RawMessage(`{"prompt":"改代码"}`))
	if err != nil || th.WorkRole != WorkThread {
		t.Fatalf("thread: %v %+v", err, th)
	}
	if l, _ := s.ListWorkSessions(ctx, alice, w.ID, 10); len(l) != 1 || l[0].ID != th.ID {
		t.Fatalf("线程列表不含协调者: %+v", l)
	}
	if err := s.DeleteSession(ctx, alice, coord.ID); !errors.Is(err, ErrSessionBusy) && !errors.Is(err, ErrCoordinator) {
		t.Fatalf("协调者不能单独删: %v", err)
	}

	// 协调者这一轮结束，令牌失效
	s.AddEvent(ctx, d.ID, cm.ID, EventDone, json.RawMessage(`{"ok":true,"output":"开了一个线程"}`), 60)
	if _, _, err := s.WorkByToken(ctx, cl.WorkToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("这一轮结束后令牌应该失效: %v", err)
	}
	if err := s.DeleteSession(ctx, alice, coord.ID); !errors.Is(err, ErrCoordinator) {
		t.Fatalf("协调者不能单独删: %v", err)
	}

	// 线程回完：协调者空闲，结果变成它的下一条消息
	if cl, _ := s.ClaimSession(ctx, d, 60); cl.Session.ID != th.ID || cl.WorkToken != "" || cl.Instructions != "先读 README" {
		t.Fatalf("claim thread: %+v", cl)
	}
	rep, err := s.ReportEvent(ctx, d.ID, tm.ID, EventDone, json.RawMessage(`{"ok":true,"output":"改好了"}`), 60)
	if err != nil || rep.Coordinator == nil || rep.Coordinator.Steer != nil || rep.Coordinator.Session.Status != SessionPending {
		t.Fatalf("notify: %v %+v", err, rep.Coordinator)
	}
	msgs, _ := s.Messages(ctx, coord.ID)
	last := msgs[len(msgs)-1]
	if last.Role != RoleUserMsg || !strings.Contains(string(last.Content), "改好了") || !strings.Contains(string(last.Content), `"from_session":"`+th.ID+`"`) {
		t.Fatalf("协调者收到的消息: %s", last.Content)
	}

	// 协调者在跑时，线程再回完就是插话
	s.ClaimSession(ctx, d, 60)
	s.AddMessage(ctx, owner, th.ID, json.RawMessage(`{"prompt":"再补个测试"}`))
	thMsgs, _ := s.Messages(ctx, th.ID)
	rep, err = s.ReportEvent(ctx, d.ID, thMsgs[len(thMsgs)-1].ID, EventDone, json.RawMessage(`{"ok":false,"error":"boom"}`), 60)
	if err == nil {
		t.Fatal("线程还没被领走，回报应该被拒")
	}
	if cl, _ := s.ClaimSession(ctx, d, 60); cl.Session.ID != th.ID {
		t.Fatalf("claim thread again: %+v", cl)
	}
	rep, err = s.ReportEvent(ctx, d.ID, thMsgs[len(thMsgs)-1].ID, EventDone, json.RawMessage(`{"ok":false,"error":"boom"}`), 60)
	if err != nil || rep.Coordinator == nil || rep.Coordinator.Steer == nil || !strings.Contains(rep.Coordinator.Steer.Prompt, "失败: boom") {
		t.Fatalf("steer to coordinator: %v %+v", err, rep.Coordinator)
	}

	// 已完成：只有 idle 能标记，再发消息就重新打开
	if _, err := s.ResolveSession(ctx, alice, coord.ID); !errors.Is(err, ErrCoordinator) {
		t.Fatalf("协调者不能标记完成: %v", err)
	}
	if ss, err := s.ResolveSession(ctx, alice, th.ID); err != nil || ss.Status != SessionResolved {
		t.Fatalf("resolve: %v %+v", err, ss)
	}
	if _, err := s.ResolveSession(ctx, alice, th.ID); err != nil {
		t.Fatalf("重复标记直接返回: %v", err)
	}
	if ss, err := s.ReopenSession(ctx, alice, th.ID); err != nil || ss.Status != SessionIdle {
		t.Fatalf("reopen: %v %+v", err, ss)
	}
	s.ResolveSession(ctx, alice, th.ID)
	if ss, _, err := s.AddMessage(ctx, alice, th.ID, json.RawMessage(`{"prompt":"again"}`)); err != nil || ss.Status != SessionPending {
		t.Fatalf("已完成的会话发消息重新打开: %v %+v", err, ss)
	}
	if _, err := s.ResolveSession(ctx, alice, th.ID); !errors.Is(err, ErrSessionState) {
		t.Fatalf("pending 不能标记完成: %v", err)
	}

	// 改说明、列表
	ins := "新说明"
	if w2, err := s.UpdateWork(ctx, alice, w.ID, WorkPatch{Instructions: &ins}); err != nil || w2.Instructions != ins {
		t.Fatalf("update: %v %+v", err, w2)
	}
	if l, _ := s.ListWorks(ctx, alice); len(l) != 1 || l[0].ThreadCount != 1 {
		t.Fatalf("list works: %+v", l)
	}
	if l, _ := s.ListWorks(ctx, bob); len(l) != 0 {
		t.Fatalf("bob 看不到: %+v", l)
	}

	// 删除工作：有会话在跑时不行;协调者和线程一起删
	if err := s.DeleteWork(ctx, alice, w.ID); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("协调者在跑，不能删: %v", err)
	}
	msgs, _ = s.Messages(ctx, coord.ID)
	s.AddEvent(ctx, d.ID, msgs[len(msgs)-1].ID, EventDone, json.RawMessage(`{"ok":true}`), 60)
	if err := s.DeleteWork(ctx, alice, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, alice, th.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("线程应该一起删掉: %v", err)
	}
	if _, err := s.GetSession(ctx, alice, coord.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("协调者应该一起删掉: %v", err)
	}
}
