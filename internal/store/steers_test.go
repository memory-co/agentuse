package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSteers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	d := registered(t, s, alice, "laptop", "claude")
	s.SetDaemonHarnessEnabled(ctx, alice, d.ID, "claude", true)
	ag, _ := s.CreateAgent(ctx, alice.ID, "", "a", d.ID, "claude", "")
	ss, m, _ := s.CreateSession(ctx, alice, ag.ID, "", json.RawMessage(`{"prompt":"x"}`))

	// pending 时就能插话，挂在还没派出的消息上
	if _, _, err := s.AddSteer(ctx, alice, ss.ID, " "); err == nil {
		t.Fatal("空插话应该报错")
	}
	_, st1, err := s.AddSteer(ctx, alice, ss.ID, "a")
	if err != nil || st1.MessageID != m.ID || st1.Status != SteerPending {
		t.Fatalf("steer: %v %+v", err, st1)
	}
	if got, _ := s.PendingSteers(ctx, m.ID); len(got) != 1 {
		t.Fatalf("pending: %+v", got)
	}
	s.ClaimSession(ctx, d, 60)
	_, st2, _ := s.AddSteer(ctx, alice, ss.ID, "b")
	_, st3, _ := s.AddSteer(ctx, alice, ss.ID, "c")

	// 送达一条;别的消息上的 steer_id 不算
	if err := s.AddEvent(ctx, d.ID, m.ID, EventSteered, json.RawMessage(`{"steer_id":`+itoa(st2.ID)+`}`), 60); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSession(ctx, alice, ss.ID); got.Status != SessionRunning {
		t.Fatalf("steered 后还在跑: %+v", got)
	}

	// 结束时没送达的(a、c)合成下一条消息，会话回到 pending
	if err := s.AddEvent(ctx, d.ID, m.ID, EventDone, json.RawMessage(`{"ok":true}`), 60); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSession(ctx, alice, ss.ID)
	msgs, _ := s.Messages(ctx, ss.ID)
	if got.Status != SessionPending || len(msgs) != 3 || msgs[1].Role != RoleAssistantMsg || string(msgs[2].Content) != `{"prompt":"a\n\nc"}` {
		t.Fatalf("queued: %+v %+v", got, msgs)
	}
	all, _ := s.Steers(ctx, ss.ID)
	if len(all) != 3 || all[0].Status != SteerQueued || all[1].Status != SteerDelivered || all[2].Status != SteerQueued || all[1].ID != st2.ID || all[2].ID != st3.ID {
		t.Fatalf("steers: %+v", all)
	}

	// 下一轮跑完，没插话就回到 idle;idle 时不能插话
	cl, _ := s.ClaimSession(ctx, d, 60)
	s.AddEvent(ctx, d.ID, cl.Message.ID, EventDone, json.RawMessage(`{"ok":true}`), 60)
	if _, _, err := s.AddSteer(ctx, alice, ss.ID, "d"); !errors.Is(err, ErrSessionIdle) {
		t.Fatalf("idle steer: %v", err)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
