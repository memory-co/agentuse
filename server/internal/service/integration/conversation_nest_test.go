package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

type nestedFixture struct {
	s                         *service.Service
	u                         service.User
	d                         service.Device
	coordinator, executor     service.Agent
	parent, child             service.Conversation
	parentInput, childInput   protocol.ConversationMessage
	parentSource, childSource service.AgentSource
}

func nested(t *testing.T) nestedFixture {
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
	if err != nil || !strings.Contains(p.Context, "policy: coordinator") {
		t.Fatal(p, err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, p, "parent-native", 1, acceptedEvent(run, "plan-input", p.Content))
	source, err := s.ExecutionSource(ctx, d.ID, parent.ID, a.ID, "parent-native", run)
	if err != nil {
		t.Fatal(err)
	}
	source.User = u
	child, reply, err := s.CreateConversation(ctx, u, parent.ID, "Execute", "implement", []string{b.ID}, nil, "single", &source)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Message.AuthorAgentID != a.ID || child.ParentAgentID != a.ID || child.Mode != "single" {
		t.Fatal(child, reply)
	}
	input, err := s.DispatchMessage(ctx, d)
	if err != nil || input.ConversationID != child.ID || !strings.Contains(input.Context, "policy: executor") {
		t.Fatal(input, err)
	}
	childRun := uuid.NewString()
	reportNative(t, s, d, input, "child-native", 1, acceptedEvent(childRun, "execute-input", input.Content))
	childSource, err := s.ExecutionSource(ctx, d.ID, child.ID, b.ID, "child-native", childRun)
	if err != nil {
		t.Fatal(err)
	}
	childSource.User = u
	return nestedFixture{s, u, d, a, b, parent, child, p, input, source, childSource}
}
func TestConversationModesAndImmutableCreationLineage(t *testing.T) {
	f := nested(t)
	ctx := context.Background()
	s := f.s
	for _, tc := range []struct {
		parent, mode string
		source       *service.AgentSource
	}{
		{f.parent.ID, "single", nil}, {"", "single", &f.parentSource}, {f.child.ID, "single", &f.childSource}, {f.parent.ID, "collaborative", &f.parentSource}, {"", "collaborative", &f.childSource},
	} {
		if _, _, err := s.CreateConversation(ctx, f.u, tc.parent, "", "", nil, nil, tc.mode, tc.source); !errors.Is(err, service.ErrConversationScope) {
			t.Fatalf("scope bypass: %+v %v", tc, err)
		}
	}
	single, _, err := s.CreateConversation(ctx, f.u, "", "Single", "direct", []string{f.coordinator.ID}, nil, "", nil)
	if err != nil || single.Mode != "single" {
		t.Fatal(single, err)
	}
	input, err := s.DispatchMessage(ctx, f.d)
	if err != nil || !strings.Contains(input.Context, "policy: single") {
		t.Fatal(input, err)
	}
	for _, mode := range []string{"multi_agent", "coordinator"} {
		if _, _, err = s.CreateConversation(ctx, f.u, "", "", "", nil, nil, mode, nil); err == nil {
			t.Fatal("invalid mode accepted", mode)
		}
	}
	for _, parts := range [][5]string{{f.d.ID, f.parent.ID, f.coordinator.ID, "wrong", f.parentSource.RunID}, {f.d.ID, f.child.ID, f.coordinator.ID, "child-native", f.childSource.RunID}, {"other-device", f.parent.ID, f.coordinator.ID, "parent-native", f.parentSource.RunID}} {
		if _, err = s.ExecutionSource(ctx, parts[0], parts[1], parts[2], parts[3], parts[4]); err == nil {
			t.Fatal("forged execution accepted", parts)
		}
	}
	roots, err := s.ListConversations(ctx, f.u, 100)
	if err != nil || len(roots) != 2 {
		t.Fatal(roots, err)
	}
	children, err := s.ListChildren(ctx, f.u, f.parent.ID, 100)
	if err != nil || len(children) != 1 || children[0].ParentAgentID != f.coordinator.ID {
		t.Fatal(children, err)
	}
	if _, err = s.SendAgentMessage(ctx, f.childSource, f.parent.ID, "bypass", []string{f.coordinator.ID}, nil, nil, ""); !errors.Is(err, service.ErrConversationScope) {
		t.Fatal(err)
	}
}
func TestChildFeedbackReplyAndFinalReturnUseOrdinaryMessages(t *testing.T) {
	f := nested(t)
	ctx := context.Background()
	s := f.s
	feedback, err := s.SendParentMessage(ctx, f.childSource, "Which branch?", nil)
	if err != nil || feedback.Delivery != "delivered" {
		t.Fatal(feedback, err)
	}
	parent, err := s.ReadConversation(ctx, f.u, f.parent.ID)
	if err != nil || len(parent.Messages) != 2 {
		t.Fatal(parent, err)
	}
	m := parent.Messages[1]
	if m.SourceConversationID != f.child.ID || m.SourceMessageID != feedback.Message.ID || m.AuthorAgentID != f.executor.ID || m.AuthorAgentVersionID != f.childSource.AgentVersionID || len(m.ToAgentIDs) != 1 || m.ToAgentIDs[0] != f.coordinator.ID || m.ParentDeliveryRequested || len(m.ToUserIDs) != 0 {
		t.Fatal(m)
	}
	if _, err = s.SendAgentMessage(ctx, f.parentSource, f.child.ID, "Use main.", []string{f.executor.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	reportNative(t, s, f.d, f.childInput, "child-native", 2, map[string]any{"type": "result", "run_id": f.childSource.RunID, "status": "completed", "input_ids": []string{"execute-input"}, "output": "Implemented."})
	for i := 0; i < 3; i++ {
		if err = s.ReconcileParentMessages(ctx); err != nil {
			t.Fatal(err)
		}
	}
	parent, err = s.ReadConversation(ctx, f.u, f.parent.ID)
	if err != nil || len(parent.Messages) != 3 || parent.Messages[2].Content != "Implemented." {
		t.Fatal(parent, err)
	}
	child, err := s.ReadConversation(ctx, f.u, f.child.ID)
	if err != nil || len(child.Messages) != 4 || child.Messages[3].ParentDelivery != "delivered" {
		t.Fatal(child, err)
	}
	if len(child.Runs) != 1 || child.Runs[0].ResultMessageID == nil || *child.Runs[0].ResultMessageID != child.Messages[3].ID {
		t.Fatal(child.Runs)
	}
}
func TestParentDeliveryRetriesWithoutRollingBackChildResult(t *testing.T) {
	f := nested(t)
	ctx := context.Background()
	s := f.s
	// A parent run may already be complete when its child returns.
	reportNative(t, s, f.d, f.parentInput, "parent-native", 2, map[string]any{"type": "result", "run_id": f.parentSource.RunID, "status": "completed", "input_ids": []string{"plan-input"}, "output": "Delegated."})
	if err := s.SetDeviceHarnessEnabled(ctx, f.u, f.d.ID, f.coordinator.Harness, false); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"type": "result", "run_id": f.childSource.RunID, "status": "failed", "input_ids": []string{"execute-input"}, "error": "Tests failed"})
	envelope := reportEnvelope(f.childInput, "child-native", 2, data)
	if err := s.ReportClientEvent(ctx, f.d.ID, envelope); err != nil {
		t.Fatal(err)
	}
	child, err := s.ReadConversation(ctx, f.u, f.child.ID)
	if err != nil || child.Runs[0].Status != "failed" || child.Messages[1].ParentDelivery != "pending" || child.Messages[1].Content != "Tests failed" {
		t.Fatal(child, err)
	}
	if err = s.SetDeviceHarnessEnabled(ctx, f.u, f.d.ID, f.coordinator.Harness, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = s.ReportClientEvent(ctx, f.d.ID, envelope); err != nil {
			t.Fatal(err)
		}
		if err = s.ReconcileParentMessages(ctx); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := s.ReadConversation(ctx, f.u, f.parent.ID)
	if err != nil || len(parent.Messages) != 3 || parent.Messages[2].SourceMessageID != child.Messages[1].ID || len(parent.Runs) != 1 {
		t.Fatal(parent, err)
	}
}
func TestChildCancellationAndRejectionAreReturnedOnce(t *testing.T) {
	for _, status := range []string{"cancelled", "rejected"} {
		t.Run(status, func(t *testing.T) {
			f := nested(t)
			ctx := context.Background()
			s := f.s
			if status == "cancelled" {
				reportNative(t, s, f.d, f.childInput, "child-native", 2, map[string]any{"type": "result", "run_id": f.childSource.RunID, "status": "cancelled", "input_ids": []string{"execute-input"}})
			} else {
				child, _, err := s.CreateConversation(ctx, f.u, f.parent.ID, "Reject", "cannot start", []string{f.executor.ID}, nil, "single", &f.parentSource)
				if err != nil {
					t.Fatal(err)
				}
				input, err := s.DispatchMessage(ctx, f.d)
				if err != nil || input.ConversationID != child.ID {
					t.Fatal(input, err)
				}
				if err = s.RejectMessage(ctx, f.d.ID, input.Key(), protocol.AckError, "preparation failed"); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 3; i++ {
				if err := s.ReconcileParentMessages(ctx); err != nil {
					t.Fatal(err)
				}
			}
			parent, err := s.ReadConversation(ctx, f.u, f.parent.ID)
			if err != nil || len(parent.Messages) != 2 || parent.Messages[1].SourceMessageID == "" {
				t.Fatal(parent, err)
			}
		})
	}
}
func TestNestedDeleteAndDeletedParentAgent(t *testing.T) {
	f := nested(t)
	ctx := context.Background()
	s := f.s
	if err := s.DeleteConversation(ctx, f.u, f.parent.ID); !errors.Is(err, service.ErrConversationChildren) {
		t.Fatal(err)
	}
	if err := s.DeleteTree(ctx, f.u, f.parent.ID); !errors.Is(err, service.ErrConversationBusy) {
		t.Fatal(err)
	}
	if err := s.DeleteAgent(ctx, f.u, f.coordinator.ID); err != nil {
		t.Fatal(err)
	}
	feedback, err := s.SendParentMessage(ctx, f.childSource, "Need help", nil)
	if err != nil || feedback.Delivery != "failed" || feedback.Error == "" {
		t.Fatal(feedback, err)
	}
	child, err := s.GetConversation(ctx, f.u, f.child.ID)
	if err != nil || child.ParentAgentID != f.coordinator.ID {
		t.Fatal("lineage was rewritten", child, err)
	}
	parent, err := s.ReadConversation(ctx, f.u, f.parent.ID)
	if err != nil || len(parent.Messages) != 1 {
		t.Fatal("rerouted to another recipient", parent, err)
	}
}

func TestEachParentAgentOwnsItsCreatedChildReturnAddress(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	b, err := s.CreateAgent(ctx, u.ID, "second-planner", "Second planner", d.ID, a.Harness, "")
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := s.CreateConversation(ctx, u, "", "Shared planners", "plan together", []string{a.ID, b.ID}, nil, "collaborative", nil)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]service.AgentSource{}
	for i := 0; i < 2; i++ {
		input, err := s.DispatchMessage(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		session := "parent-" + input.AgentID
		run := uuid.NewString()
		reportNative(t, s, d, input, session, 1, acceptedEvent(run, "input", input.Content))
		source, err := s.ExecutionSource(ctx, d.ID, parent.ID, input.AgentID, session, run)
		if err != nil {
			t.Fatal(err)
		}
		source.User = u
		sources[input.AgentID] = source
	}
	for _, actor := range []service.Agent{a, b} {
		source := sources[actor.ID]
		child, _, err := s.CreateConversation(ctx, u, parent.ID, "Execute", "perform task", []string{a.ID}, nil, "single", &source)
		if err != nil || child.ParentAgentID != actor.ID {
			t.Fatal(child, err)
		}
		input, err := s.DispatchMessage(ctx, d)
		if err != nil || input.ConversationID != child.ID {
			t.Fatal(input, err)
		}
		reportNative(t, s, d, input, "child-"+child.ID, 1, acceptedEvent(uuid.NewString(), "input", input.Content))
		runs, err := s.Runs(ctx, child.ID)
		if err != nil || len(runs) != 1 {
			t.Fatal(runs, err)
		}
		childSource, err := s.ExecutionSource(ctx, d.ID, child.ID, a.ID, "child-"+child.ID, runs[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		childSource.User = u
		response, err := s.SendParentMessage(ctx, childSource, "A question", nil)
		if err != nil || response.Delivery != "delivered" {
			t.Fatal(response, err)
		}
		messages, err := s.Messages(ctx, parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		m := messages[len(messages)-1]
		if len(m.ToAgentIDs) != 1 || m.ToAgentIDs[0] != actor.ID {
			t.Fatal("message recipients replaced creation lineage", m)
		}
		// Avoid taking the queued parent feedback when dispatching the next child.
		feedback, err := s.DispatchMessage(ctx, d)
		if err != nil || feedback.ConversationID != parent.ID || feedback.AgentID != actor.ID {
			t.Fatal(feedback, err)
		}
	}
}

func TestDeletingParentAgentRefreshesPendingFeedbackProjection(t *testing.T) {
	f := nested(t)
	ctx := context.Background()
	s := f.s
	if err := s.SetDeviceHarnessEnabled(ctx, f.u, f.d.ID, f.coordinator.Harness, false); err != nil {
		t.Fatal(err)
	}
	feedback, err := s.SendParentMessage(ctx, f.childSource, "Need clarification", nil)
	if err != nil || feedback.Delivery != "pending" {
		t.Fatal(feedback, err)
	}
	subscription, closeSubscription := s.BrowserChanges.Subscribe(f.u)
	defer closeSubscription()
	if err = s.DeleteAgent(ctx, f.u, f.coordinator.ID); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range subscription.Drain() {
		if change.Resource == "conversations" && change.ID == f.child.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("parent deletion did not refresh source feedback status")
	}
	messages, err := s.Messages(ctx, f.child.ID)
	if err != nil || messages[len(messages)-1].ParentDelivery != "failed" {
		t.Fatal(messages, err)
	}
}
