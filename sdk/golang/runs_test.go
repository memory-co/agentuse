package agentuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWaitMessageRunsRequiresEveryRecipientToSettle(t *testing.T) {
	message := Message{ID: "message", ToAgentIDs: []string{"a", "b"}}
	a := Run{ID: "run-a", AgentID: "a", Status: "completed"}
	input := RunInput{RunID: a.ID, InputID: "input-a", MessageID: message.ID}
	unknown := message
	unknown.AgentErrors = map[string]MessageError{"b": {Error: "receipt lost", Uncertain: true}}
	rejected := message
	rejected.AgentErrors = map[string]MessageError{"b": {Error: "init failed"}}
	snapshots := []ConversationDetail{
		{Messages: []Message{message}},
		{Messages: []Message{message}, Runs: []Run{a}, RunInputs: []RunInput{input}},
		{Messages: []Message{unknown}, Runs: []Run{a}, RunInputs: []RunInput{input}},
		{Messages: []Message{rejected}, Runs: []Run{a}, RunInputs: []RunInput{input}},
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := calls
		if index >= len(snapshots) {
			index = len(snapshots) - 1
		}
		calls++
		json.NewEncoder(w).Encode(snapshots[index])
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	detail, err := New(server.URL, "test").WaitMessageRuns(ctx, "", "conversation", message.ID, time.Millisecond)
	if err != nil || calls != len(snapshots) || detail.Messages[0].AgentErrors["b"].Error != "init failed" {
		t.Fatal("wait returned before every recipient settled", calls, detail, err)
	}
}
