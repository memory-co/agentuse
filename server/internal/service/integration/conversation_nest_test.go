package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

type roundFixture struct {
	s                     *service.Service
	u                     service.User
	d                     service.Device
	parent, child         service.Conversation
	coordinator, executor service.Agent
	source                service.AgentSource
	parentInput           protocol.ConversationMessage
	parentRun             string
	inputs                map[string]protocol.ConversationMessage
	runs                  map[string]string
}

func roundSetup(t *testing.T, multi, accept bool) roundFixture {
	t.Helper()
	s, u, d, a := runStore(t)
	ctx := context.Background()
	b, err := s.CreateAgent(ctx, u.ID, "executor", "Executor", d.ID, a.Harness, "")
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := s.CreateConversation(ctx, u, "", "Coordinate", "plan", []string{a.ID}, nil, "collaborative", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	parentRun := uuid.NewString()
	reportNative(t, s, d, p, "parent-native", 1, acceptedEvent(parentRun, "plan-input", p.Content))
	source, err := s.ExecutionSource(ctx, d.ID, parent.ID, a.ID, "parent-native", parentRun)
	if err != nil {
		t.Fatal(err)
	}
	source.User = u
	targets := []string{b.ID}
	if multi {
		targets = append(targets, a.ID)
	}
	child, reply, err := s.CreateConversation(ctx, u, parent.ID, "Execute", "implement", targets, nil, "single", &source)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentAgentID != a.ID || reply.Message.AuthorAgentID != a.ID || reply.Round == nil || reply.Round.Seq != 1 {
		t.Fatal(child, reply)
	}
	f := roundFixture{s: s, u: u, d: d, parent: parent, child: child, coordinator: a, executor: b, source: source, parentInput: p, parentRun: parentRun, inputs: map[string]protocol.ConversationMessage{}, runs: map[string]string{}}
	for range targets {
		input, err := s.DispatchMessage(ctx, d)
		if err != nil || input.ConversationID != child.ID {
			t.Fatal(input, err)
		}
		f.inputs[input.AgentID] = input
		if accept {
			run := uuid.NewString()
			f.runs[input.AgentID] = run
			reportNative(t, s, d, input, "child-"+input.AgentID, 1, acceptedEvent(run, "initial", input.Content))
		}
	}
	return f
}
func (f roundFixture) result(t *testing.T, agent, status, text string) {
	t.Helper()
	reportNative(t, f.s, f.d, f.inputs[agent], "child-"+agent, 2, map[string]any{"type": "result", "run_id": f.runs[agent], "input_ids": []string{"initial"}, "status": status, "output": text})
}
func parentNotices(t *testing.T, f roundFixture) []service.Message {
	t.Helper()
	messages, err := f.s.Messages(context.Background(), f.parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := []service.Message{}
	for _, m := range messages {
		if m.AuthorRole == "child_conversation" {
			out = append(out, m)
		}
	}
	return out
}
func TestConversationRoundWaitsForEveryAgentAndPendingInput(t *testing.T) {
	f := roundSetup(t, true, true)
	ctx := context.Background()
	s := f.s
	f.result(t, f.executor.ID, "completed", "Executor result")
	if got := parentNotices(t, f); len(got) != 0 {
		t.Fatal("reported partial execution", got)
	}
	extra, err := s.SendAgentMessage(ctx, f.source, f.child.ID, "also verify", []string{f.executor.ID}, nil, nil, "")
	if err != nil || extra.Round == nil || extra.Round.Seq != 1 {
		t.Fatal(extra, err)
	}
	f.result(t, f.coordinator.ID, "completed", "Same Agent, other conversation")
	if got := parentNotices(t, f); len(got) != 0 {
		t.Fatal("ignored pending input", got)
	}
	input, err := s.DispatchMessage(ctx, f.d)
	if err != nil || input.MessageID != extra.Message.ID {
		t.Fatal(input, err)
	}
	run := uuid.NewString()
	reportNative(t, s, f.d, input, "child-"+f.executor.ID, 3, acceptedEvent(run, "extra", input.Content))
	reportNative(t, s, f.d, input, "child-"+f.executor.ID, 4, map[string]any{"type": "result", "run_id": run, "input_ids": []string{"extra"}, "status": "completed", "output": "Verified"})
	notices := parentNotices(t, f)
	if len(notices) != 1 {
		t.Fatal(notices)
	}
	n := notices[0]
	if n.AuthorConversationID != f.child.ID || n.AuthorRoundSeq != 1 || n.AuthorAgentID != "" || n.AuthorAgentVersionID != "" || n.AuthorUserID != "" || len(n.ToAgentIDs) != 1 || n.ToAgentIDs[0] != f.coordinator.ID || len(n.ToUserIDs) != 0 || n.Content == "Verified" {
		t.Fatal(n)
	}
	child, err := s.ReadConversation(ctx, f.u, f.child.ID)
	if err != nil || len(child.Rounds) != 1 || child.Rounds[0].FinishedAt == nil || child.Rounds[0].ParentDelivery != "delivered" {
		t.Fatal(child, err)
	}
	for _, m := range child.Messages {
		if m.AuthorRole != "agent" || m.AuthorConversationID != "" {
			t.Fatal("child history was replaced by notices", m)
		}
	}
	for i := 0; i < 3; i++ {
		if err = s.ReconcileRoundNotifications(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(parentNotices(t, f)) != 1 {
		t.Fatal("notification replay duplicated")
	}
}
func TestFinishedRoundsSurviveNotificationFailureAndHaveFixedSnapshots(t *testing.T) {
	f := roundSetup(t, false, true)
	ctx := context.Background()
	s := f.s
	reportNative(t, s, f.d, f.parentInput, "parent-native", 2, map[string]any{"type": "result", "run_id": f.parentRun, "input_ids": []string{"plan-input"}, "status": "completed", "output": "Delegated"})
	if _, err := s.DB.Exec(`CREATE TRIGGER block_round_notice BEFORE INSERT ON messages WHEN NEW.author_role='child_conversation' BEGIN SELECT RAISE(FAIL,'parent unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	f.result(t, f.executor.ID, "completed", "Round one result")
	first, err := s.ReadRound(ctx, f.u, f.child.ID, 1)
	if err != nil || first.Rounds[0].FinishedAt == nil || first.Rounds[0].ParentDelivery != "pending" || first.Runs[0].Status != "completed" {
		t.Fatal(first, err)
	}
	second, err := s.SendAgentMessage(ctx, f.source, f.child.ID, "Second round", []string{f.executor.ID}, nil, nil, "")
	if err != nil || second.Round == nil || second.Round.Seq != 2 {
		t.Fatal(second, err)
	}
	input, err := s.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, f.d, input, "child-"+f.executor.ID, 3, acceptedEvent(run, "second", input.Content))
	raw, _ := json.Marshal(map[string]any{"type": "result", "run_id": run, "input_ids": []string{"second"}, "status": "failed", "error": "Tests failed"})
	envelope := reportEnvelope(input, "child-"+f.executor.ID, 4, raw)
	if err = s.ReportClientEvent(ctx, f.d.ID, envelope); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER block_round_notice`); err != nil {
		t.Fatal(err)
	}
	restarted := service.New(s.Repository)
	for i := 0; i < 3; i++ {
		if err = restarted.ReconcileRoundNotifications(ctx); err != nil {
			t.Fatal(err)
		}
		if err = s.ReportClientEvent(ctx, f.d.ID, envelope); err != nil {
			t.Fatal(err)
		}
	}
	notices := parentNotices(t, f)
	if len(notices) != 2 || notices[0].AuthorRoundSeq != 1 || notices[1].AuthorRoundSeq != 2 {
		t.Fatal(notices)
	}
	snapshot, err := s.ReadRound(ctx, f.u, f.child.ID, 1)
	if err != nil || len(snapshot.Messages) != 2 || len(snapshot.Runs) != 1 || snapshot.Messages[1].Content != "Round one result" || *snapshot.Rounds[0].EndMessageSeq != *first.Rounds[0].EndMessageSeq {
		t.Fatal(snapshot, err)
	}
	snapshot, err = s.ReadRound(ctx, f.u, f.child.ID, 2)
	if err != nil || len(snapshot.Messages) != 2 || snapshot.Messages[1].Content != "Tests failed" || len(snapshot.Deliveries) != 1 || len(snapshot.Runs) != 1 || len(snapshot.Events) != 2 {
		t.Fatal(snapshot, err)
	}
	if _, err = s.ReadRound(ctx, f.u, f.child.ID, 3); !errors.Is(err, service.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestRoundWaitingOfflineAndUnknownAreNotCompletion(t *testing.T) {
	t.Run("native waiting and offline", func(t *testing.T) {
		f := roundSetup(t, false, true)
		ctx := context.Background()
		reportNative(t, f.s, f.d, f.inputs[f.executor.ID], "child-"+f.executor.ID, 2, map[string]any{"type": "request", "run_id": f.runs[f.executor.ID], "request_id": "permission", "state": "pending", "kind": "permission", "message": "Allow?"})
		f.s.DeviceDisconnected(ctx, f.d.ID)
		if err := f.s.ReconcileRoundNotifications(ctx); err != nil {
			t.Fatal(err)
		}
		detail, err := f.s.ReadConversation(ctx, f.u, f.child.ID)
		if err != nil || detail.Rounds[0].FinishedAt != nil || len(parentNotices(t, f)) != 0 {
			t.Fatal(detail, err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		f := roundSetup(t, false, false)
		ctx := context.Background()
		if err := f.s.RejectMessage(ctx, f.d.ID, f.inputs[f.executor.ID].Key(), protocol.AckUnknown, "outcome uncertain"); err != nil {
			t.Fatal(err)
		}
		detail, err := f.s.ReadConversation(ctx, f.u, f.child.ID)
		if err != nil || detail.Rounds[0].FinishedAt != nil || len(parentNotices(t, f)) != 0 {
			t.Fatal(detail, err)
		}
	})
}
func TestRejectedFailedAndCancelledRoundsAlwaysNotify(t *testing.T) {
	for _, status := range []string{"rejected", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			f := roundSetup(t, false, status != "rejected")
			ctx := context.Background()
			if status == "rejected" {
				if err := f.s.RejectMessage(ctx, f.d.ID, f.inputs[f.executor.ID].Key(), protocol.AckError, "preparation failed"); err != nil {
					t.Fatal(err)
				}
			} else {
				f.result(t, f.executor.ID, status, "")
			}
			notices := parentNotices(t, f)
			if len(notices) != 1 {
				t.Fatal(notices)
			}
			detail, err := f.s.ReadRound(ctx, f.u, f.child.ID, 1)
			if err != nil || detail.Rounds[0].FinishedAt == nil {
				t.Fatal(detail, err)
			}
			if status == "rejected" && (len(detail.Runs) != 0 || len(detail.Messages) != 1 || detail.Deliveries[0].Error != "preparation failed") {
				t.Fatal("invented a run or Agent reply for rejection", detail)
			}
		})
	}
}
func TestEmptyConversationsAndNotesDoNotProduceRounds(t *testing.T) {
	f := roundSetup(t, false, true)
	ctx := context.Background()
	s := f.s
	empty, _, err := s.CreateConversation(ctx, f.u, f.parent.ID, "Empty", "", nil, nil, "single", &f.source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, f.u, empty.ID, "note", nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveConversation(ctx, f.u, empty.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.ReadConversation(ctx, f.u, empty.ID)
	if err != nil || len(detail.Rounds) != 0 || len(parentNotices(t, f)) != 0 {
		t.Fatal(detail, err)
	}
}
func TestRoundDeliveryFailurePreservesLineageAndDeleteGuards(t *testing.T) {
	f := roundSetup(t, false, true)
	ctx := context.Background()
	s := f.s
	if err := s.DeleteConversation(ctx, f.u, f.parent.ID); !errors.Is(err, service.ErrConversationChildren) {
		t.Fatal(err)
	}
	if err := s.DeleteTree(ctx, f.u, f.parent.ID); !errors.Is(err, service.ErrConversationBusy) {
		t.Fatal(err)
	}
	if err := s.SetDeviceHarnessEnabled(ctx, f.u, f.d.ID, f.coordinator.Harness, false); err != nil {
		t.Fatal(err)
	}
	f.result(t, f.executor.ID, "completed", "Need a decision; this round is finished")
	detail, err := s.ReadRound(ctx, f.u, f.child.ID, 1)
	if err != nil || detail.Rounds[0].ParentDelivery != "pending" {
		t.Fatal(detail, err)
	}
	if err = s.DeleteConversation(ctx, f.u, f.child.ID); !errors.Is(err, service.ErrConversationBusy) {
		t.Fatal("lost an undelivered finished round", err)
	}
	sub, stop := s.BrowserChanges.Subscribe(f.u)
	defer stop()
	if err = s.DeleteAgent(ctx, f.u, f.coordinator.ID); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, change := range sub.Drain() {
		if change.Resource == "conversations" && change.ID == f.child.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatal("delivery projection was not invalidated")
	}
	detail, err = s.ReadRound(ctx, f.u, f.child.ID, 1)
	if err != nil || detail.Rounds[0].ParentDelivery != "failed" || detail.Conversation.ParentAgentID != f.coordinator.ID || len(parentNotices(t, f)) != 0 {
		t.Fatal(detail, err)
	}
}
func TestConversationModesAndExecutionSourcesStillConstrainCreation(t *testing.T) {
	f := roundSetup(t, false, true)
	ctx := context.Background()
	s := f.s
	childSource, err := s.ExecutionSource(ctx, f.d.ID, f.child.ID, f.executor.ID, "child-"+f.executor.ID, f.runs[f.executor.ID])
	if err != nil {
		t.Fatal(err)
	}
	childSource.User = f.u
	for _, tc := range []struct {
		parent, mode string
		source       *service.AgentSource
	}{{f.parent.ID, "single", nil}, {"", "single", &f.source}, {f.child.ID, "single", &childSource}, {f.parent.ID, "collaborative", &f.source}, {"", "single", &childSource}} {
		if _, _, err = s.CreateConversation(ctx, f.u, tc.parent, "", "", nil, nil, tc.mode, tc.source); !errors.Is(err, service.ErrConversationScope) {
			t.Fatal(tc, err)
		}
	}
	if _, err = s.SendAgentMessage(ctx, childSource, f.parent.ID, "manual parent message", []string{f.coordinator.ID}, nil, nil, ""); !errors.Is(err, service.ErrConversationScope) {
		t.Fatal(err)
	}
	if _, err = s.ExecutionSource(ctx, f.d.ID, f.child.ID, f.executor.ID, "forged", f.runs[f.executor.ID]); err == nil {
		t.Fatal("forged execution source")
	}
	input := f.inputs[f.executor.ID]
	if !strings.Contains(input.Context, "end this round normally") || strings.Contains(input.Context, "send --parent") {
		t.Fatal(input.Context)
	}
}

// Finishing the interval is part of the result transaction, not a best-effort
// callback. An unavailable parent is separate, but a failed source commit retries.
func TestRoundBoundaryFailureRollsBackTheWholeSourceResult(t *testing.T) {
	f := roundSetup(t, false, true)
	ctx := context.Background()
	if _, err := f.s.DB.Exec(`CREATE TRIGGER block_round_end BEFORE UPDATE ON conversation_rounds WHEN NEW.finished_at IS NOT NULL BEGIN SELECT RAISE(FAIL,'source boundary unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"type": "result", "run_id": f.runs[f.executor.ID], "input_ids": []string{"initial"}, "status": "completed", "output": "durable result"})
	envelope := reportEnvelope(f.inputs[f.executor.ID], "child-"+f.executor.ID, 2, raw)
	if err := f.s.ReportClientEvent(ctx, f.d.ID, envelope); err == nil {
		t.Fatal("source failure was acknowledged")
	}
	detail, err := f.s.ReadRound(ctx, f.u, f.child.ID, 1)
	if err != nil || len(detail.Messages) != 1 || len(detail.Events) != 1 || detail.Runs[0].Status != "running" || detail.Rounds[0].FinishedAt != nil || len(parentNotices(t, f)) != 0 {
		t.Fatal(detail, err)
	}
	if _, err = f.s.DB.Exec(`DROP TRIGGER block_round_end`); err != nil {
		t.Fatal(err)
	}
	if err = f.s.ReportClientEvent(ctx, f.d.ID, envelope); err != nil {
		t.Fatal(err)
	}
	detail, err = f.s.ReadRound(ctx, f.u, f.child.ID, 1)
	if err != nil || len(detail.Messages) != 2 || detail.Runs[0].Status != "completed" || detail.Rounds[0].FinishedAt == nil || len(parentNotices(t, f)) != 1 {
		t.Fatal(detail, err)
	}
}

func TestConcurrentAgentResultsNotifyOnce(t *testing.T) {
	f := roundSetup(t, true, true)
	ctx := context.Background()
	f.s.Now = time.Now
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, agent := range []string{f.executor.ID, f.coordinator.ID} {
		go func(agent string) {
			<-start
			raw, _ := json.Marshal(map[string]any{"type": "result", "run_id": f.runs[agent], "input_ids": []string{"initial"}, "status": "completed", "output": agent + " result"})
			results <- f.s.ReportClientEvent(ctx, f.d.ID, reportEnvelope(f.inputs[agent], "child-"+agent, 2, raw))
		}(agent)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	detail, err := f.s.ReadRound(ctx, f.u, f.child.ID, 1)
	if err != nil || len(detail.Messages) != 3 || detail.Rounds[0].FinishedAt == nil || len(parentNotices(t, f)) != 1 {
		t.Fatal(detail, err)
	}
}

func TestConcurrentFinalResultAndNewInputPreserveTheRoundBoundary(t *testing.T) {
	f := roundSetup(t, false, true)
	ctx := context.Background()
	f.s.Now = time.Now
	start := make(chan struct{})
	type sent struct {
		reply service.Reply
		err   error
	}
	input := make(chan sent, 1)
	result := make(chan error, 1)
	go func() {
		<-start
		reply, err := f.s.SendMessage(ctx, f.u, f.child.ID, "Concurrent follow-up", []string{f.executor.ID}, nil, nil, "")
		input <- sent{reply, err}
	}()
	go func() {
		<-start
		raw, _ := json.Marshal(map[string]any{"type": "result", "run_id": f.runs[f.executor.ID], "input_ids": []string{"initial"}, "status": "completed", "output": "First result"})
		result <- f.s.ReportClientEvent(ctx, f.d.ID, reportEnvelope(f.inputs[f.executor.ID], "child-"+f.executor.ID, 2, raw))
	}()
	close(start)
	send := <-input
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if send.err != nil || send.reply.Round == nil {
		t.Fatal(send)
	}
	round := send.reply.Round.Seq
	detail, err := f.s.ReadConversation(ctx, f.u, f.child.ID)
	if err != nil || len(detail.Rounds) != int(round) || len(parentNotices(t, f)) != int(round-1) || detail.Rounds[len(detail.Rounds)-1].FinishedAt != nil {
		t.Fatal("input joined an already finished interval, or was omitted from an active one", detail, err)
	}
	var next protocol.ConversationMessage
	for {
		next, err = f.s.DispatchMessage(ctx, f.d)
		if err != nil {
			t.Fatal(err)
		}
		if next.MessageID == send.reply.Message.ID {
			break
		}
	}
	run := uuid.NewString()
	reportNative(t, f.s, f.d, next, "child-"+f.executor.ID, 3, acceptedEvent(run, "next", next.Content))
	reportNative(t, f.s, f.d, next, "child-"+f.executor.ID, 4, map[string]any{"type": "result", "run_id": run, "input_ids": []string{"next"}, "status": "completed", "output": "Follow-up result"})
	if len(parentNotices(t, f)) != int(round) {
		t.Fatal("lost or duplicated a round notification")
	}
	if round == 2 {
		first, err := f.s.ReadRound(ctx, f.u, f.child.ID, 1)
		if err != nil || len(first.Messages) != 2 || first.Messages[1].Content != "First result" {
			t.Fatal("new input crossed the finished boundary", first, err)
		}
	}
}

func TestEachCreatingAgentReceivesOnlyItsChildRoundNotifications(t *testing.T) {
	f := roundSetup(t, false, true)
	ctx := context.Background()
	if _, err := f.s.SendMessage(ctx, f.u, f.parent.ID, "Coordinate another branch", []string{f.executor.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	parentInput, err := f.s.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	parentRun := uuid.NewString()
	reportNative(t, f.s, f.d, parentInput, "second-parent-agent", 1, acceptedEvent(parentRun, "coordinate", parentInput.Content))
	source, err := f.s.ExecutionSource(ctx, f.d.ID, f.parent.ID, f.executor.ID, "second-parent-agent", parentRun)
	if err != nil {
		t.Fatal(err)
	}
	source.User = f.u
	child, _, err := f.s.CreateConversation(ctx, f.u, f.parent.ID, "Second branch", "Execute", []string{f.coordinator.ID}, nil, "single", &source)
	if err != nil || child.ParentAgentID != f.executor.ID {
		t.Fatal(child, err)
	}
	input, err := f.s.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, f.s, f.d, input, "second-branch", 1, acceptedEvent(run, "execute", input.Content))
	reportNative(t, f.s, f.d, input, "second-branch", 2, map[string]any{"type": "result", "run_id": run, "input_ids": []string{"execute"}, "status": "completed", "output": "Second branch result"})
	f.result(t, f.executor.ID, "completed", "First branch result")
	notices := parentNotices(t, f)
	if len(notices) != 2 {
		t.Fatal(notices)
	}
	for _, notice := range notices {
		want := f.coordinator.ID
		if notice.AuthorConversationID == child.ID {
			want = f.executor.ID
		}
		if len(notice.ToAgentIDs) != 1 || notice.ToAgentIDs[0] != want || notice.AuthorAgentID != "" {
			t.Fatal("mixed up the creator and executor identities", notice)
		}
	}
}
