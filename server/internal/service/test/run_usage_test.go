package test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestRunUsagePreservesNativeMeasurements(t *testing.T) {
	count := func(n int64) *int64 { return &n }
	for _, test := range []struct {
		name, status, usage string
		want                *agentuse.RunUsage
	}{
		{"full", "completed", `{"input_tokens":12345,"output_tokens":321,"cache_read_tokens":6789,"cache_write_tokens":100}`, &agentuse.RunUsage{InputTokens: count(12345), OutputTokens: count(321), CacheReadTokens: count(6789), CacheWriteTokens: count(100)}},
		{"zero", "completed", `{"input_tokens":0,"output_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0}`, &agentuse.RunUsage{InputTokens: count(0), OutputTokens: count(0), CacheReadTokens: count(0), CacheWriteTokens: count(0)}},
		{"partial", "completed", `{"input_tokens":42,"cache_write_tokens":0}`, &agentuse.RunUsage{InputTokens: count(42), CacheWriteTokens: count(0)}},
		{"cache_only", "completed", `{"cache_read_tokens":100}`, &agentuse.RunUsage{CacheReadTokens: count(100)}},
		{"null", "completed", `null`, nil},
		{"missing", "completed", "", nil},
		{"failed", "failed", `{"output_tokens":3}`, &agentuse.RunUsage{OutputTokens: count(3)}},
		{"cancelled", "cancelled", `{"input_tokens":7}`, &agentuse.RunUsage{InputTokens: count(7)}},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			running, err := s.RunService.GetRun(ctx, u, conversation.ID, id)
			if err != nil || running.Usage != nil {
				t.Fatal("unreported usage must be unknown", running, err)
			}
			payload := map[string]any{"type": "result", "run_id": id, "status": test.status, "input_ids": []string{"input"}, "output": "done"}
			if test.usage != "" {
				payload["usage"] = json.RawMessage(test.usage)
			}
			envelope := reportNative(t, s, d, input, "native", 2, payload)
			actual, err := s.RunService.GetRun(ctx, u, conversation.ID, id)
			if err != nil || !reflect.DeepEqual(actual.Usage, test.want) {
				t.Fatal("native counters were changed or filled with zero", actual.Usage, err)
			}
			if string(actual.Result) != string(envelope.Event) {
				t.Fatal("original result was changed", string(actual.Result))
			}
			wire, _ := json.Marshal(actual)
			var fields map[string]json.RawMessage
			json.Unmarshal(wire, &fields)
			want, _ := json.Marshal(test.want)
			if string(fields["usage"]) != string(want) {
				t.Fatal("Run must expose nullable usage and nullable counters", string(wire))
			}
			detail, err := s.ConversationService.ReadConversation(ctx, u, conversation.ID)
			if err != nil || len(detail.Runs) != 1 || !reflect.DeepEqual(detail.Runs[0].Usage, test.want) {
				t.Fatal("conversation snapshot lost usage", detail, err)
			}
		})
	}
}

func TestInvalidResultUsageDoesNotPartiallyCommit(t *testing.T) {
	for _, usage := range []string{
		`{"input_tokens":-1}`, `{"output_tokens":-1}`, `{"cache_read_tokens":-1}`, `{"cache_write_tokens":-1}`,
		`{"input_tokens":1.5}`, `{"cache_read_tokens":"123"}`, `{"output_tokens":true}`, `[]`, `"unknown"`,
	} {
		t.Run(usage, func(t *testing.T) {
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
			raw, _ := json.Marshal(map[string]any{"type": "result", "run_id": id, "status": "completed", "input_ids": []string{"input"}, "output": "done", "usage": json.RawMessage(usage)})
			if err = s.ConversationService.ReportClientEvent(ctx, d.ID, reportEnvelope(input, "native", 2, raw)); err == nil {
				t.Fatal("invalid usage accepted", usage)
			}
			detail, err := s.ConversationService.ReadConversation(ctx, u, conversation.ID)
			if err != nil || len(detail.Events) != 1 || len(detail.Messages) != 1 || len(detail.Runs) != 1 || detail.Runs[0].Status != "running" || detail.Runs[0].Usage != nil {
				t.Fatal("invalid usage partially committed", detail, err)
			}
		})
	}
}
