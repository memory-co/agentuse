package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestWaitChildUsesFixedRoundsAndYieldsForParentInterruptions(t *testing.T) {
	for _, reason := range []string{"new_input", "needs_input", "settled", "run_ended", "timeout"} {
		t.Run(reason, func(t *testing.T) {
			parent := sdk.ConversationDetail{Conversation: sdk.Conversation{ID: "parent", Mode: "collaborative", Status: "running"}, Runs: []sdk.Run{{ID: "run", AgentID: "planner", Status: "running"}}}
			child := sdk.ConversationDetail{Conversation: sdk.Conversation{ID: "child", ParentConversationID: "parent", Mode: "single", Status: "running"}, Messages: []sdk.Message{{Seq: 1, ToAgentIDs: []string{"executor"}}}, Rounds: []sdk.ConversationRound{{ConversationID: "child", Seq: 1, StartMessageSeq: 1}}}
			switch reason {
			case "new_input":
				parent.Deliveries = []sdk.Delivery{{AgentID: "planner", Status: "pending"}}
			case "needs_input":
				parent.Questions = []sdk.Question{{RunID: "run", State: "pending"}}
			case "settled":
				child.Conversation.Status = "running"
				end := int64(2)
				child.Rounds[0].FinishedAt = &end
				child.Rounds[0].EndMessageSeq = &end
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
			response, err := c.WaitChild(context.Background(), sdk.New(server.URL, ""), "child", 0, 60*time.Millisecond)
			if err != nil || response.Reason != reason || response.Detail.Conversation.ID != "child" {
				t.Fatal(response, err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("wait blocked the answer path")
			}
		})
	}
}

func TestWaitChildKeepsItsTargetWhenNextRoundHasStarted(t *testing.T) {
	for _, explicit := range []int64{0, 1} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			var roundReads int
			ended := int64(2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/conversations/parent" {
					json.NewEncoder(w).Encode(sdk.ConversationDetail{Conversation: sdk.Conversation{ID: "parent"}})
					return
				}
				if r.URL.Query().Get("round") == "" {
					json.NewEncoder(w).Encode(sdk.ConversationDetail{Conversation: sdk.Conversation{ID: "child", ParentConversationID: "parent"}, Rounds: []sdk.ConversationRound{{Seq: 1}}})
					return
				}
				if r.URL.Query().Get("round") != "1" {
					t.Error("wait changed target", r.URL)
				}
				roundReads++
				round := sdk.ConversationRound{Seq: 1}
				if roundReads >= 2 {
					round.FinishedAt = &ended
					round.EndMessageSeq = &ended
				}
				// The conversation is busy with the next round, the selected one is done.
				json.NewEncoder(w).Encode(sdk.ConversationDetail{Conversation: sdk.Conversation{ID: "child", Status: "running"}, Rounds: []sdk.ConversationRound{round}})
			}))
			defer server.Close()
			c := &ExecutionContext{ConversationID: "parent"}
			result, err := c.WaitChild(context.Background(), sdk.New(server.URL, ""), "child", explicit, time.Second)
			if err != nil || result.Reason != "settled" || result.Detail.Rounds[0].Seq != 1 || roundReads != 2 {
				t.Fatal(result, err, roundReads)
			}
		})
	}
}
