package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestWaitChildYieldsForFeedbackAndParentInterruptions(t *testing.T) {
	for _, reason := range []string{"parent_feedback", "new_input", "needs_input", "settled", "run_ended", "timeout"} {
		t.Run(reason, func(t *testing.T) {
			parent := sdk.ConversationDetail{Conversation: sdk.Conversation{ID: "parent", Mode: "collaborative", Status: "running"}, Runs: []sdk.Run{{ID: "run", AgentID: "planner", Status: "running"}}}
			child := sdk.ConversationDetail{Conversation: sdk.Conversation{ID: "child", ParentConversationID: "parent", Mode: "single", Status: "running"}, Messages: []sdk.Message{{Seq: 1, ToAgentIDs: []string{"executor"}}}}
			switch reason {
			case "parent_feedback":
				child.Messages = append(child.Messages, sdk.Message{Seq: 2, ParentDeliveryRequested: true, Content: "Question"})
			case "new_input":
				parent.Deliveries = []sdk.Delivery{{AgentID: "planner", Status: "pending"}}
			case "needs_input":
				parent.Questions = []sdk.Question{{RunID: "run", State: "pending"}}
			case "settled":
				child.Conversation.Status = "idle"
			case "run_ended":
				parent.Runs[0].Status = "cancelled"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("wait issued mutation")
				}
				if r.URL.Path == "/api/conversations/parent" {
					json.NewEncoder(w).Encode(parent)
				} else {
					json.NewEncoder(w).Encode(child)
				}
			}))
			defer server.Close()
			c := &ExecutionContext{ConversationID: "parent", AgentID: "planner", RunID: "run"}
			start := time.Now()
			response, err := c.WaitChild(context.Background(), sdk.New(server.URL, ""), "child", 60*time.Millisecond)
			if err != nil || response.Reason != reason || response.Detail.Conversation.ID != "child" {
				t.Fatal(response, err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("wait blocked the answer path")
			}
		})
	}
}
