package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const askPayload = `{"tool_use_id":"t1","questions":[{"question":"语言？","options":[{"label":"Go"},{"label":"Python"}]}]}`

func TestQuestions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	bob, _, _ := s.CreateUser(ctx, "", "bob", RoleUser)
	d := registered(t, s, alice, "laptop", "claude")
	s.SetDaemonHarnessEnabled(ctx, alice, d.ID, "claude", true)
	ag, _ := s.CreateAgent(ctx, alice.ID, "", "a", d.ID, "claude", "")
	ss, m, _ := s.CreateSession(ctx, alice, ag.ID, "", json.RawMessage(`{"prompt":"x"}`))
	s.ClaimSession(ctx, d, 60)

	if err := s.AddEvent(ctx, d.ID, m.ID, EventQuestion, json.RawMessage(`{"questions":[]}`), 60); err == nil {
		t.Fatal("没有 tool_use_id 应该报错")
	}
	// 提问：会话变成 waiting;同一个 tool_use_id 重发只记一次
	for range 2 {
		if err := s.AddEvent(ctx, d.ID, m.ID, EventQuestion, json.RawMessage(askPayload), 60); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetSession(ctx, alice, ss.ID)
	qs, _ := s.Questions(ctx, ss.ID)
	if got.Status != SessionWaiting || len(qs) != 1 || qs[0].Status != QuestionPending || qs[0].ToolUseID != "t1" {
		t.Fatalf("question: %+v %+v", got, qs)
	}
	if err := s.DeleteSession(ctx, alice, ss.ID); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("waiting 时不能删: %v", err)
	}
	// waiting 时也续租、也能插话
	if err := s.RenewLease(ctx, d.ID, m.ID, 60); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddSteer(ctx, alice, ss.ID, "顺便"); err != nil {
		t.Fatal(err)
	}

	// 回答：别人答不了，缺答案报错
	if _, _, err := s.AnswerQuestion(ctx, bob, ss.ID, qs[0].ID, map[string]string{"语言？": "Go"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob: %v", err)
	}
	if _, _, err := s.AnswerQuestion(ctx, alice, ss.ID, qs[0].ID, map[string]string{"别的": "Go"}); err == nil {
		t.Fatal("缺答案应该报错")
	}
	_, q, err := s.AnswerQuestion(ctx, alice, ss.ID, qs[0].ID, map[string]string{"语言？": " Go "})
	if err != nil || q.Answers["语言？"] != "Go" || q.Status != QuestionPending {
		t.Fatalf("answer: %v %+v", err, q)
	}
	if _, _, err := s.AnswerQuestion(ctx, alice, ss.ID, qs[0].ID, map[string]string{"语言？": "Python"}); !errors.Is(err, ErrQuestionClosed) {
		t.Fatalf("答两次: %v", err)
	}
	if got, _ := s.PendingAnswers(ctx, d.ID); len(got) != 1 {
		t.Fatalf("pending answers: %+v", got)
	}

	// 送到：问题 answered,会话回到 running
	if err := s.AddEvent(ctx, d.ID, m.ID, EventAnswered, json.RawMessage(`{"tool_use_id":"t1"}`), 60); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSession(ctx, alice, ss.ID)
	qs, _ = s.Questions(ctx, ss.ID)
	if got.Status != SessionRunning || qs[0].Status != QuestionAnswered {
		t.Fatalf("answered: %+v %+v", got, qs)
	}
	if got, _ := s.PendingAnswers(ctx, d.ID); len(got) != 0 {
		t.Fatalf("pending answers: %+v", got)
	}

	// 第二次提问超时：答了没送到的答案转成插话
	s.AddEvent(ctx, d.ID, m.ID, EventQuestion, json.RawMessage(strings.Replace(askPayload, "t1", "t2", 1)), 60)
	qs, _ = s.Questions(ctx, ss.ID)
	s.AnswerQuestion(ctx, alice, ss.ID, qs[1].ID, map[string]string{"语言？": "Python"})
	steers, err := s.ReportEvent(ctx, d.ID, m.ID, EventQuestionExpired, json.RawMessage(`{"tool_use_id":"t2"}`), 60)
	if err != nil || len(steers) != 1 || !strings.Contains(steers[0].Prompt, "语言？ → Python") || steers[0].MessageID != m.ID {
		t.Fatalf("expired: %v %+v", err, steers)
	}
	got, _ = s.GetSession(ctx, alice, ss.ID)
	qs, _ = s.Questions(ctx, ss.ID)
	if got.Status != SessionRunning || qs[1].Status != QuestionExpired {
		t.Fatalf("expired: %+v %+v", got, qs)
	}

	// 第三次提问时进程结束：问题作废，插话一起转成下一条消息
	s.AddEvent(ctx, d.ID, m.ID, EventQuestion, json.RawMessage(strings.Replace(askPayload, "t1", "t3", 1)), 60)
	if err := s.AddEvent(ctx, d.ID, m.ID, EventDone, json.RawMessage(`{"ok":true}`), 60); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSession(ctx, alice, ss.ID)
	qs, _ = s.Questions(ctx, ss.ID)
	msgs, _ := s.Messages(ctx, ss.ID)
	if got.Status != SessionPending || qs[2].Status != QuestionExpired || !strings.Contains(string(msgs[2].Content), "顺便") || !strings.Contains(string(msgs[2].Content), "Python") {
		t.Fatalf("done: %+v %+v %s", got, qs, msgs[2].Content)
	}
}

func TestQuestionRequeue(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	alice, _, _ := s.CreateUser(ctx, "", "alice", RoleUser)
	d := registered(t, s, alice, "laptop", "claude")
	s.SetDaemonHarnessEnabled(ctx, alice, d.ID, "claude", true)
	ag, _ := s.CreateAgent(ctx, alice.ID, "", "a", d.ID, "claude", "")
	ss, m, _ := s.CreateSession(ctx, alice, ag.ID, "", json.RawMessage(`{"prompt":"x"}`))
	s.ClaimSession(ctx, d, 60)
	s.AddEvent(ctx, d.ID, m.ID, EventQuestion, json.RawMessage(askPayload), 60)
	qs, _ := s.Questions(ctx, ss.ID)
	s.AnswerQuestion(ctx, alice, ss.ID, qs[0].ID, map[string]string{"语言？": "Go"})

	// daemon 重连时没在跑这条:会话收回，问题作废，答案转成挂在同一条消息上的插话
	if users, err := s.RequeueDaemonSessions(ctx, d.ID, nil); err != nil || len(users) != 1 {
		t.Fatalf("requeue: %v %v", err, users)
	}
	got, _ := s.GetSession(ctx, alice, ss.ID)
	qs, _ = s.Questions(ctx, ss.ID)
	steers, _ := s.PendingSteers(ctx, m.ID)
	if got.Status != SessionPending || qs[0].Status != QuestionExpired || len(steers) != 1 {
		t.Fatalf("requeue: %+v %+v %+v", got, qs, steers)
	}
}
