package test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestRunModelComesFromNativeResultIncludingHarnessDefaults(t *testing.T) {
	for _, test := range []struct {
		name, status, configured string
		reported                 any
		want                     string
	}{
		{"harness_default", "completed", "", "native-default-model", "native-default-model"},
		{"resolved_alias", "completed", "configured-alias", "native-resolved-model", "native-resolved-model"},
		{"failed_default", "failed", "", "native-default-model", "native-default-model"},
		{"cancelled_default", "cancelled", "", "native-default-model", "native-default-model"},
		{"unknown_default", "completed", "", nil, ""},
		{"unknown_with_config", "completed", "configured-alias", nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, u, d, a := runStore(t)
			ctx := context.Background()
			if test.configured != "" {
				var err error
				a, err = s.AgentService.PublishAgentVersion(ctx, u, a.ID, a.CurrentVersionID, agentuse.AgentConfig{Name: a.Name, DeviceID: d.ID, Harness: a.Harness, Model: test.configured, Skills: []agentuse.SkillRef{}})
				if err != nil {
					t.Fatal(err)
				}
			}
			conversation, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "hello", []string{a.ID}, nil, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			input, err := s.RunService.DispatchMessage(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			reportNative(t, s, d, input, "native", 1, acceptedEvent(id, "input", input.Content))
			running, err := s.RunService.GetRun(ctx, u, conversation.ID, id)
			if err != nil || running.Model != nil {
				t.Fatal("configured model was presented as an execution fact", running, err)
			}
			payload := map[string]any{"type": "result", "run_id": id, "status": test.status, "input_ids": []string{"input"}, "output": "done", "model": test.reported}
			if test.status == "failed" {
				payload["error"] = "native execution failed"
			}
			reportNative(t, s, d, input, "native", 2, payload)
			actual, err := s.RunService.GetRun(ctx, u, conversation.ID, id)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if actual.Model != nil {
					t.Fatal("unknown native model was filled from configuration", actual)
				}
			} else if actual.Model == nil || *actual.Model != test.want {
				t.Fatal("native execution model was lost", actual)
			}
			wire, _ := json.Marshal(actual)
			var fields map[string]any
			json.Unmarshal(wire, &fields)
			if _, ok := fields["model"]; !ok {
				t.Fatal("Run must expose model even when null", string(wire))
			}
			detail, err := s.ConversationService.ReadConversation(ctx, u, conversation.ID)
			if err != nil || len(detail.Runs) != 1 || (test.want != "" && (detail.Runs[0].Model == nil || *detail.Runs[0].Model != test.want)) {
				t.Fatal(detail, err)
			}
		})
	}
}

func TestInvalidResultModelDoesNotCommitAnUnreadableRun(t *testing.T) {
	for _, model := range []any{"", "   ", 42, map[string]any{"name": "native"}} {
		s, u, d, a := runStore(t)
		ctx := context.Background()
		conversation, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "hello", []string{a.ID}, nil, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		input, err := s.RunService.DispatchMessage(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		reportNative(t, s, d, input, "native", 1, acceptedEvent(id, "input", input.Content))
		raw, _ := json.Marshal(map[string]any{"type": "result", "run_id": id, "status": "completed", "input_ids": []string{"input"}, "output": "done", "model": model})
		if err = s.ConversationService.ReportClientEvent(ctx, d.ID, reportEnvelope(input, "native", 2, raw)); err == nil {
			t.Fatal("invalid native model accepted", model)
		}
		detail, err := s.ConversationService.ReadConversation(ctx, u, conversation.ID)
		if err != nil || len(detail.Events) != 1 || len(detail.Messages) != 1 || detail.Runs[0].Status != "running" || detail.Runs[0].Model != nil {
			t.Fatal("invalid model partially committed", detail, err)
		}
	}
}
