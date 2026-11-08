package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestCompleteMessagesUsesActualSessionAndAcceptedBoundary(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	ss, _, err := s.CreateMultiSession(ctx, u, "", "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	send := func(text string, targets ...string) Message {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"prompt": text})
		r, e := s.SendMultiMessage(ctx, u, ss.ID, raw, targets, nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		return *r.Message
	}
	oldNote := send("note-before-first")
	first := send("first-agent-input", a.ID)
	p, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	futureNote := send("posted-after-first-input")
	firstContext, err := s.CompleteMessages(ctx, d.ID, ss.ID, first.ID, a.ID, "native-a")
	if err != nil || len(firstContext.CompleteMessages) != 1 || firstContext.CompleteMessages[0].ID != oldNote.ID {
		t.Fatal(firstContext, err)
	}
	if strings.Contains(p.Context, "first-agent-input") || strings.Contains(p.Context, "共享对话历史") {
		t.Fatal("dispatch already duplicated history", p.Context)
	}
	r1 := uuid.NewString()
	reportNative(t, s, d, p, "native-a", 1, acceptedEvent(r1, "a1", p.Content))
	reportNative(t, s, d, p, "native-a", 2, map[string]any{"type": "result", "run_id": r1, "input_ids": []string{"a1"}, "status": "completed", "output": "a-own-reply"})
	b, err := s.CreateAgent(ctx, u.ID, "other", "Other", d.ID, a.Harness, "")
	if err != nil {
		t.Fatal(err)
	}
	bInput := send("other-agent-input", b.ID)
	bp, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	rb := uuid.NewString()
	reportNative(t, s, d, bp, "native-b", 1, acceptedEvent(rb, "b1", bp.Content))
	reportNative(t, s, d, bp, "native-b", 2, map[string]any{"type": "result", "run_id": rb, "input_ids": []string{"b1"}, "status": "completed", "output": "other-agent-answer", "usage": map[string]int{"input_tokens": 12345}})
	detail, _ := s.ReadSession(ctx, u, ss.ID)
	bReply := detail.Messages[len(detail.Messages)-1]
	note := send("unaddressed shared note")
	current := send("current-agent-input", a.ID)
	cp, err := s.DispatchMessage(ctx, d)
	if err != nil || cp.MessageID != current.ID {
		t.Fatal(cp, err)
	}
	send("future-must-not-leak")
	view, err := s.CompleteMessages(ctx, d.ID, ss.ID, current.ID, a.ID, "native-a")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, m := range view.CompleteMessages {
		ids = append(ids, m.ID)
	}
	want := []string{futureNote.ID, bInput.ID, bReply.ID, note.ID}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("missing/duplicate context: %v want %v", ids, want)
	}
	if view.CompleteMessages[2].Text != "other-agent-answer" || view.CompleteMessages[2].AuthorID != b.ID || view.CompleteMessages[2].AuthorRole != "agent" {
		t.Fatal(view)
	}
	data, _ := json.Marshal(view)
	if strings.Contains(string(data), "input_tokens") {
		t.Fatal("copied execution metadata into context", string(data))
	}
	fresh, err := s.CompleteMessages(ctx, d.ID, ss.ID, current.ID, a.ID, "fresh-native-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.CompleteMessages) != int(current.Seq)-1 {
		t.Fatalf("fresh session lost history: %+v", fresh)
	}
	if _, err = s.CompleteMessages(ctx, d.ID, ss.ID, current.ID, a.ID, "native-b"); err == nil {
		t.Fatal("cross-agent session accepted")
	}
	if _, err = s.CompleteMessages(ctx, "other-device", ss.ID, current.ID, a.ID, "native-a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unassigned device obtained context", err)
	}
	// A new version needs full context; an old native address cannot be rebound.
	next, err := s.PublishAgentVersion(ctx, u, a.ID, a.CurrentVersionID, agentuse.AgentConfig{Name: a.Name, DaemonID: d.ID, Harness: a.Harness, Instructions: "version-two"})
	if err != nil {
		t.Fatal(err)
	}
	nextInput := send("new-version-input", a.ID)
	np, err := s.DispatchMessage(ctx, d)
	if err != nil || np.AgentVersionID != next.CurrentVersionID {
		t.Fatal(np, err)
	}
	if _, err = s.CompleteMessages(ctx, d.ID, ss.ID, nextInput.ID, a.ID, "native-a"); err == nil {
		t.Fatal("old version session accepted")
	}
	nv, err := s.CompleteMessages(ctx, d.ID, ss.ID, nextInput.ID, a.ID, "native-a-v2")
	if err != nil || len(nv.CompleteMessages) != int(nextInput.Seq)-1 {
		t.Fatal(nv, err)
	}
}
